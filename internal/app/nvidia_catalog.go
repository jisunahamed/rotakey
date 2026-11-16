package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

//go:embed catalog/nvidia.json
var nvidiaCatalogJSON []byte

type nvidiaCatalogEntry struct {
	ID         string   `json:"id"`
	Source     string   `json:"source_url"`
	Context    int      `json:"context_window"`
	Input      []string `json:"input_modalities"`
	Tools      *bool    `json:"tools"`
	Reasoning  *bool    `json:"reasoning"`
	Structured *bool    `json:"structured_output"`
}

func nvidiaFacts(provider Provider, model string) map[string]string {
	u, e := url.Parse(provider.BaseURL)
	if e != nil || !(strings.EqualFold(u.Hostname(), "integrate.api.nvidia.com") || strings.EqualFold(u.Hostname(), "ai.api.nvidia.com")) {
		return nil
	}
	var catalog struct {
		Models []nvidiaCatalogEntry `json:"models"`
	}
	if json.Unmarshal(nvidiaCatalogJSON, &catalog) != nil {
		return nil
	}
	for _, entry := range catalog.Models {
		if entry.ID != model {
			continue
		}
		facts := map[string]string{"context_window": strconv.Itoa(entry.Context), "context_source": entry.Source, "capability_source": "nvidia_official_catalog"}
		for k, value := range map[string]*bool{"tools": entry.Tools, "thinking": entry.Reasoning, "json_output": entry.Structured} {
			if value != nil {
				facts[k] = "unsupported"
				if *value {
					facts[k] = "catalog_supported"
				}
			}
		}
		facts["images"] = "unsupported"
		for _, modality := range entry.Input {
			if modality == "Image" {
				facts["images"] = "catalog_supported"
			}
		}
		switch model {
		case "openai/gpt-oss-20b", "mistralai/mistral-large-4-1t-a52b":
			facts["reasoning_levels"] = "low,medium,high"
		case "z-ai/glm-5.3-flash":
			// GLM always thinks; its documented effort budget is not a
			// switch for disabling thinking, nor does it support medium/xhigh.
			facts["reasoning_levels"] = "low,high,max"
			facts["reasoning_source"] = "https://docs.nvidia.com/nim/vision-language-models/latest/get-started/advanced/get-started-glm-5-3-flash.html"
		case "nvidia/nemotron-3-ultra-550b-a55b", "nvidia/nemotron-3.5-bolt-e4b", "nvidia/nemotron-3.5-lightning-30b-a3b", "nvidia/nemotron-3-nano-omni-30b-a3b-reasoning":
			facts["thinking_control"] = "chat_template_kwargs.enable_thinking"
		}
		return facts
	}
	return nil
}

// Refresh exact NVIDIA model identities only. Provider-declared/manual context
// limits win over the model-card fallback; inferred context does not become a
// claim that the hosted endpoint accepted a request of that size.
func (s *Server) refreshNvidiaFacts(ctx context.Context) error {
	rows, err := s.db.Query(ctx, `SELECT m.id,p.base_url,m.upstream_model,m.capability_profile FROM model_routes m JOIN providers p ON p.id=m.provider_id`)
	if err != nil {
		return err
	}
	type change struct {
		id       string
		profile  map[string]string
		original []byte
	}
	changes := []change{}
	for rows.Next() {
		var id, base, model string
		var raw []byte
		if err = rows.Scan(&id, &base, &model, &raw); err != nil {
			rows.Close()
			return err
		}
		facts := nvidiaFacts(Provider{BaseURL: base}, model)
		if facts == nil {
			continue
		}
		profile := map[string]string{}
		if err = json.Unmarshal(raw, &profile); err != nil {
			rows.Close()
			return err
		}
		preserveContext := profile["context_window"] != "" && profile["context_source"] == ""
		for k, v := range facts {
			if (k == "context_window" || k == "context_source") && preserveContext {
				continue
			}
			profile[k] = v
		}
		changes = append(changes, change{id, profile, raw})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, c := range changes {
		raw, _ := json.Marshal(c.profile)
		if _, err = tx.Exec(ctx, `UPDATE model_routes SET capability_profile=$2 WHERE id=$1 AND capability_profile=$3`, c.id, raw, c.original); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
