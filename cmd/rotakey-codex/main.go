package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	version     = "0.2.5"
	beginMarker = "# BEGIN ROTAKEY CODEX MANAGED BLOCK"
	endMarker   = "# END ROTAKEY CODEX MANAGED BLOCK"
)

type state struct {
	GatewayURL   string `json:"gateway_url"`
	DefaultModel string `json:"default_model"`
	SecretStore  string `json:"secret_store"`
	InstalledAt  string `json:"installed_at"`
	CodexVersion string `json:"codex_version"`
}

type manifest struct {
	Models []manifestModel `json:"models"`
}

type manifestModel struct {
	ID                      string   `json:"id"`
	Alias                   string   `json:"alias"`
	DisplayName             string   `json:"display_name"`
	ContextWindow           int      `json:"context_window"`
	SupportsResponses       bool     `json:"supports_responses"`
	SupportsTools           bool     `json:"supports_tools"`
	SupportsImages          bool     `json:"supports_images"`
	VerifiedReasoningLevels []string `json:"verified_reasoning_levels"`
	CatalogReady            bool     `json:"catalog_ready"`
}

type catalog struct {
	Models []catalogModel `json:"models"`
}

type catalogModel struct {
	Slug                     string           `json:"slug"`
	DisplayName              string           `json:"display_name"`
	Description              string           `json:"description"`
	DefaultReasoningLevel    string           `json:"default_reasoning_level"`
	SupportedReasoningLevels []reasoningLevel `json:"supported_reasoning_levels"`
	ShellType                string           `json:"shell_type"`
	Visibility               string           `json:"visibility"`
	SupportedInAPI           bool             `json:"supported_in_api"`
	Priority                 int              `json:"priority"`
	BaseInstructions         string           `json:"base_instructions"`
	SupportsReasoningSummary bool             `json:"supports_reasoning_summaries"`
	DefaultReasoningSummary  string           `json:"default_reasoning_summary"`
	SupportVerbosity         bool             `json:"support_verbosity"`
	DefaultVerbosity         string           `json:"default_verbosity"`
	ContextWindow            int              `json:"context_window"`
	MaxContextWindow         int              `json:"max_context_window"`
	EffectiveContextPercent  int              `json:"effective_context_window_percent"`
	InputModalities          []string         `json:"input_modalities"`
	SupportsParallelTools    bool             `json:"supports_parallel_tool_calls"`
	SupportsWebSearch        bool             `json:"supports_search_tool"`
	TruncationPolicy         truncationPolicy `json:"truncation_policy"`
	ExperimentalTools        []string         `json:"experimental_supported_tools"`
}

type truncationPolicy struct {
	Mode  string `json:"mode"`
	Limit int    `json:"limit"`
}

type reasoningLevel struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "install":
		err = install(os.Args[2:])
	case "sync":
		err = syncCatalog(false)
	case "doctor":
		err = doctor(os.Args[2:])
	case "status":
		err = status()
	case "token":
		err = printToken()
	case "disable":
		err = disable()
	case "rollback":
		err = rollback()
	case "uninstall":
		err = uninstall()
	case "version", "--version", "-v":
		fmt.Println("rotakey-codex", version)
	default:
		usage()
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rotakey-codex:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: rotakey-codex <install|sync|doctor|status|disable|rollback|uninstall|version>")
}

func install(args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	gateway := flags.String("url", "", "Rotakey gateway URL")
	key := flags.String("key", "", "gateway key (prefer interactive prompt or ROTAKEY_API_KEY)")
	model := flags.String("model", "", "default public model alias")
	if err := flags.Parse(args); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	if *gateway == "" {
		fmt.Print("Rotakey URL (https://ai.example.com): ")
		value, _ := reader.ReadString('\n')
		*gateway = strings.TrimSpace(value)
	}
	if *key == "" {
		*key = os.Getenv("ROTAKEY_API_KEY")
	}
	if *key == "" {
		fmt.Print("Rotakey gateway key: ")
		value, err := readPassword(reader)
		fmt.Println()
		if err != nil {
			return err
		}
		*key = strings.TrimSpace(value)
	}
	base, err := normalizeGatewayURL(*gateway)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*key) == "" {
		return errors.New("gateway key is required")
	}
	if err := probeReady(base); err != nil {
		return fmt.Errorf("gateway is not ready: %w", err)
	}
	if err := probeModels(base, *key); err != nil {
		return fmt.Errorf("model endpoint check failed: %w", err)
	}
	if err := ensureDirs(); err != nil {
		return err
	}
	models, err := fetchManifest(base, *key)
	if err != nil {
		return err
	}
	if len(models.Models) == 0 {
		return errors.New("manifest contains no Codex-ready models; enable and validate a model route first")
	}
	selected := strings.TrimSpace(*model)
	if selected == "" {
		selected = models.Models[0].ID
	}
	if !containsModel(models, selected) {
		return fmt.Errorf("default model %q is not in the ready manifest", selected)
	}
	store, err := saveSecret(*key)
	if err != nil {
		return err
	}
	if err := writeCatalog(models); err != nil {
		return err
	}
	codexVersion := commandOutput("codex", "--version")
	current := state{GatewayURL: base, DefaultModel: selected, SecretStore: store, InstalledAt: time.Now().UTC().Format(time.RFC3339), CodexVersion: codexVersion}
	if err := writeState(current); err != nil {
		return err
	}
	if err := installConfig(current); err != nil {
		return err
	}
	fmt.Printf("Installed Rotakey profile with %d ready model(s).\n", len(models.Models))
	fmt.Printf("Start Codex with: codex --profile rotakey -m %s\n", selected)
	return nil
}

func readPassword(fallback *bufio.Reader) (string, error) {
	if runtime.GOOS == "windows" {
		script := `$s=Read-Host -AsSecureString; $b=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($s); try {[Runtime.InteropServices.Marshal]::PtrToStringBSTR($b)} finally {[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($b)}`
		command := exec.Command("powershell", "-NoProfile", "-Command", script)
		command.Stdin = os.Stdin
		command.Stderr = os.Stderr
		output, err := command.Output()
		if err == nil {
			return strings.TrimSpace(string(output)), nil
		}
	}
	if runtime.GOOS != "windows" {
		command := exec.Command("sh", "-c", "stty -echo; IFS= read -r secret; stty echo; printf %s \"$secret\"")
		command.Stdin = os.Stdin
		command.Stderr = os.Stderr
		output, err := command.Output()
		if err == nil {
			return string(output), nil
		}
	}
	value, err := fallback.ReadString('\n')
	return strings.TrimSpace(value), err
}

func syncCatalog(quiet bool) error {
	current, err := readState()
	if err != nil {
		return err
	}
	key, err := loadSecret(current.SecretStore)
	if err != nil {
		return err
	}
	models, err := fetchManifest(current.GatewayURL, key)
	if err != nil {
		return err
	}
	if len(models.Models) == 0 {
		return errors.New("manifest contains no Codex-ready models")
	}
	if err := writeCatalog(models); err != nil {
		return err
	}
	if !containsModel(models, current.DefaultModel) {
		current.DefaultModel = models.Models[0].ID
	}
	current.CodexVersion = commandOutput("codex", "--version")
	if err := writeState(current); err != nil {
		return err
	}
	if err := installConfig(current); err != nil {
		return err
	}
	if !quiet {
		fmt.Printf("Synced %d Codex-ready model(s). Restart Codex to refresh the picker.\n", len(models.Models))
	}
	return nil
}

func doctor(args []string) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	smoke := flags.Bool("smoke-test", false, "run a billed Responses API smoke test")
	if err := flags.Parse(args); err != nil {
		return err
	}
	current, err := readState()
	if err != nil {
		return check("installation state", err)
	}
	failed := false
	report := func(name string, err error) {
		if err != nil {
			failed = true
			fmt.Printf("FAIL  %-24s %v\n", name, err)
		} else {
			fmt.Printf("PASS  %s\n", name)
		}
	}
	report("Codex CLI", commandExists("codex"))
	report("managed config", verifyConfig())
	key, secretErr := loadSecret(current.SecretStore)
	report("protected gateway key", secretErr)
	if secretErr == nil {
		report("gateway readiness", probeReady(current.GatewayURL))
		models, manifestErr := fetchManifest(current.GatewayURL, key)
		report("gateway authentication", manifestErr)
		report("OpenAI model endpoint", probeModels(current.GatewayURL, key))
		if manifestErr == nil {
			if len(models.Models) == 0 {
				report("Codex model manifest", errors.New("no ready models"))
			} else {
				report("Codex model manifest", nil)
			}
		}
		report("model catalog", verifyCatalog())
		if *smoke {
			report("billed response smoke test", smokeTest(current, key))
		}
	}
	if failed {
		return errors.New("one or more checks failed")
	}
	return nil
}

func status() error {
	current, err := readState()
	if err != nil {
		return err
	}
	fmt.Println("Status: installed")
	fmt.Println("Gateway:", current.GatewayURL)
	fmt.Println("Default model:", current.DefaultModel)
	fmt.Println("Secret store:", current.SecretStore)
	fmt.Println("Codex profile: rotakey")
	fmt.Println("Run: codex --profile rotakey")
	return nil
}

func disable() error {
	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cleaned, found, err := removeManagedBlock(string(body))
	if err != nil {
		return err
	}
	if !found {
		fmt.Println("Rotakey profile is already disabled.")
		return nil
	}
	if err := backupConfig(configPath, body); err != nil {
		return err
	}
	if err := atomicWrite(configPath, []byte(cleaned), 0600); err != nil {
		return err
	}
	fmt.Println("Rotakey profile disabled. State and protected key were retained.")
	return nil
}

func rollback() error {
	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}
	backups, _ := filepath.Glob(configPath + ".rotakey-backup-*")
	if len(backups) == 0 {
		return errors.New("no Rotakey config backup exists")
	}
	sort.Strings(backups)
	latest := backups[len(backups)-1]
	body, err := os.ReadFile(latest)
	if err != nil {
		return err
	}
	if err := atomicWrite(configPath, body, 0600); err != nil {
		return err
	}
	fmt.Println("Restored", latest)
	return nil
}

func uninstall() error {
	_ = disable()
	current, _ := readState()
	if current.SecretStore != "" {
		_ = deleteSecret(current.SecretStore)
	}
	dir, err := dataDir()
	if err != nil {
		return err
	}
	for _, name := range []string{"state.json", "models.json", "secret.dpapi", "secret"} {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	fmt.Println("Rotakey Codex integration uninstalled. Config backups were retained.")
	return nil
}

func printToken() error {
	current, err := readState()
	if err != nil {
		return err
	}
	key, err := loadSecret(current.SecretStore)
	if err != nil {
		return err
	}
	fmt.Print(key)
	return nil
}

func fetchManifest(base, key string) (manifest, error) {
	var result manifest
	request, err := http.NewRequest(http.MethodGet, base+"/v1/codex/manifest", nil)
	if err != nil {
		return result, err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := client().Do(request)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return result, fmt.Errorf("manifest returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	ready := result.Models[:0]
	for _, model := range result.Models {
		if model.ID != "" && model.CatalogReady && (model.SupportsResponses || model.SupportsTools) {
			ready = append(ready, model)
		}
	}
	result.Models = ready
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	return result, nil
}

func writeCatalog(source manifest) error {
	result := catalog{Models: make([]catalogModel, 0, len(source.Models))}
	for index, model := range source.Models {
		window := model.ContextWindow
		if window <= 0 {
			window = 128000
		}
		levels := model.VerifiedReasoningLevels
		if len(levels) == 0 {
			levels = []string{"low", "medium", "high"}
		}
		reasoning := make([]reasoningLevel, 0, len(levels))
		for _, level := range levels {
			reasoning = append(reasoning, reasoningLevel{Effort: level, Description: strings.Title(level) + " reasoning"})
		}
		defaultLevel := "medium"
		if !contains(levels, defaultLevel) {
			defaultLevel = levels[0]
		}
		modalities := []string{"text"}
		if model.SupportsImages {
			modalities = append(modalities, "image")
		}
		name := model.DisplayName
		if name == "" {
			name = model.ID
		}
		result.Models = append(result.Models, catalogModel{
			Slug: model.ID, DisplayName: name, Description: "Rotakey model route " + model.ID,
			DefaultReasoningLevel: defaultLevel, SupportedReasoningLevels: reasoning,
			ShellType: "shell_command", Visibility: "list", SupportedInAPI: true, Priority: 1000 + index,
			BaseInstructions:         "You are Codex, a coding agent. Work with the user in the current workspace until the task is complete.",
			SupportsReasoningSummary: true, DefaultReasoningSummary: "none", SupportVerbosity: true,
			DefaultVerbosity: "low", ContextWindow: window, MaxContextWindow: window,
			EffectiveContextPercent: 95, InputModalities: modalities,
			SupportsParallelTools: model.SupportsTools, SupportsWebSearch: false,
			TruncationPolicy: truncationPolicy{Mode: "tokens", Limit: 10000}, ExperimentalTools: []string{},
		})
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	dir, _ := dataDir()
	return atomicWrite(filepath.Join(dir, "models.json"), append(body, '\n'), 0600)
}

func installConfig(current state) error {
	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}
	body, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	cleaned, _, err := removeManagedBlock(string(body))
	if err != nil {
		return err
	}
	if err := backupConfig(configPath, body); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, _ := dataDir()
	block := fmt.Sprintf(`%s
[model_providers.rotakey]
name = "Rotakey"
base_url = %s
wire_api = "responses"
supports_websockets = false
request_max_retries = 1
stream_max_retries = 1

[model_providers.rotakey.auth]
command = %s
args = ["token"]
timeout_ms = 5000
refresh_interval_ms = 0

[profiles.rotakey]
model_provider = "rotakey"
model = %s
model_catalog_json = %s
%s
`, beginMarker, tomlQuote(current.GatewayURL+"/v1"), tomlQuote(exe), tomlQuote(current.DefaultModel), tomlQuote(filepath.Join(dir, "models.json")), endMarker)
	if strings.TrimSpace(cleaned) != "" {
		cleaned = strings.TrimRight(cleaned, "\r\n") + "\n\n"
	}
	return atomicWrite(configPath, []byte(cleaned+block), 0600)
}

func removeManagedBlock(input string) (string, bool, error) {
	start := strings.Index(input, beginMarker)
	end := strings.Index(input, endMarker)
	if start < 0 && end < 0 {
		return input, false, nil
	}
	if start < 0 || end < start {
		return input, false, errors.New("incomplete Rotakey managed config block; restore a backup before retrying")
	}
	end += len(endMarker)
	if end < len(input) && input[end] == '\r' {
		end++
	}
	if end < len(input) && input[end] == '\n' {
		end++
	}
	return input[:start] + input[end:], true, nil
}

func saveSecret(secret string) (string, error) {
	dir, _ := dataDir()
	switch runtime.GOOS {
	case "windows":
		script := `[void][Reflection.Assembly]::LoadWithPartialName('System.Security'); [Convert]::ToBase64String([System.Security.Cryptography.ProtectedData]::Protect([Text.Encoding]::UTF8.GetBytes($env:ROTAKEY_CODEX_SECRET),$null,[System.Security.Cryptography.DataProtectionScope]::CurrentUser))`
		command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		command.Env = append(os.Environ(), "ROTAKEY_CODEX_SECRET="+secret)
		output, err := command.Output()
		if err != nil {
			return "", fmt.Errorf("Windows DPAPI encryption failed: %w", err)
		}
		if err := atomicWrite(filepath.Join(dir, "secret.dpapi"), bytes.TrimSpace(output), 0600); err != nil {
			return "", err
		}
		return "windows-dpapi", nil
	case "darwin":
		command := exec.Command("security", "add-generic-password", "-a", os.Getenv("USER"), "-s", "rotakey-codex", "-w", secret, "-U")
		if output, err := command.CombinedOutput(); err != nil {
			return "", fmt.Errorf("macOS Keychain failed: %s", strings.TrimSpace(string(output)))
		}
		return "macos-keychain", nil
	default:
		if _, err := exec.LookPath("secret-tool"); err == nil {
			command := exec.Command("secret-tool", "store", "--label=Rotakey Codex gateway key", "service", "rotakey-codex", "user", os.Getenv("USER"))
			command.Stdin = strings.NewReader(secret)
			if err := command.Run(); err == nil {
				return "linux-secret-service", nil
			}
		}
		fmt.Fprintln(os.Stderr, "warning: Secret Service unavailable; using a permission-checked 0600 key file")
		if err := atomicWrite(filepath.Join(dir, "secret"), []byte(secret), 0600); err != nil {
			return "", err
		}
		return "file-0600", nil
	}
}

func loadSecret(store string) (string, error) {
	dir, _ := dataDir()
	switch store {
	case "windows-dpapi":
		ciphertext, err := os.ReadFile(filepath.Join(dir, "secret.dpapi"))
		if err != nil {
			return "", err
		}
		script := `[void][Reflection.Assembly]::LoadWithPartialName('System.Security'); $raw=[Convert]::FromBase64String($env:ROTAKEY_CODEX_CIPHER); [Text.Encoding]::UTF8.GetString([System.Security.Cryptography.ProtectedData]::Unprotect($raw,$null,[System.Security.Cryptography.DataProtectionScope]::CurrentUser))`
		command := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
		command.Env = append(os.Environ(), "ROTAKEY_CODEX_CIPHER="+strings.TrimSpace(string(ciphertext)))
		output, err := command.Output()
		return strings.TrimSpace(string(output)), err
	case "macos-keychain":
		output, err := exec.Command("security", "find-generic-password", "-a", os.Getenv("USER"), "-s", "rotakey-codex", "-w").Output()
		return strings.TrimSpace(string(output)), err
	case "linux-secret-service":
		output, err := exec.Command("secret-tool", "lookup", "service", "rotakey-codex", "user", os.Getenv("USER")).Output()
		return strings.TrimSpace(string(output)), err
	case "file-0600":
		path := filepath.Join(dir, "secret")
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.Mode().Perm()&0077 != 0 {
			return "", errors.New("fallback key file permissions are broader than 0600")
		}
		body, err := os.ReadFile(path)
		return strings.TrimSpace(string(body)), err
	default:
		return "", fmt.Errorf("unknown secret store %q", store)
	}
}

func deleteSecret(store string) error {
	dir, _ := dataDir()
	switch store {
	case "windows-dpapi":
		return removeIfExists(filepath.Join(dir, "secret.dpapi"))
	case "macos-keychain":
		return exec.Command("security", "delete-generic-password", "-a", os.Getenv("USER"), "-s", "rotakey-codex").Run()
	case "linux-secret-service":
		return exec.Command("secret-tool", "clear", "service", "rotakey-codex", "user", os.Getenv("USER")).Run()
	case "file-0600":
		return removeIfExists(filepath.Join(dir, "secret"))
	}
	return nil
}

func normalizeGatewayURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	raw = strings.TrimSuffix(raw, "/v1")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errors.New("gateway URL must be an absolute http(s) URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" && host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", errors.New("remote Rotakey gateways must use HTTPS")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("gateway URL cannot contain a query or fragment")
	}
	return raw, nil
}

func probeReady(base string) error {
	response, err := client().Get(base + "/health/ready")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func probeModels(base, key string) error {
	request, err := http.NewRequest(http.MethodGet, base+"/v1/models", nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return nil
}

func smokeTest(current state, key string) error {
	body, _ := json.Marshal(map[string]any{"model": current.DefaultModel, "input": "Reply with OK only.", "max_output_tokens": 8})
	request, _ := http.NewRequest(http.MethodPost, current.GatewayURL+"/v1/responses", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := client().Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
	}
	return nil
}

func verifyConfig() error {
	path, _ := codexConfigPath()
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Count(string(body), beginMarker) != 1 || strings.Count(string(body), endMarker) != 1 {
		return errors.New("managed block is missing or duplicated")
	}
	return nil
}

func verifyCatalog() error {
	dir, _ := dataDir()
	body, err := os.ReadFile(filepath.Join(dir, "models.json"))
	if err != nil {
		return err
	}
	var parsed catalog
	if err := json.Unmarshal(body, &parsed); err != nil {
		return err
	}
	if len(parsed.Models) == 0 {
		return errors.New("catalog is empty")
	}
	return nil
}

func containsModel(source manifest, id string) bool {
	for _, model := range source.Models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func ensureDirs() error {
	dir, err := dataDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	configPath, err := codexConfigPath()
	if err != nil {
		return err
	}
	return os.MkdirAll(filepath.Dir(configPath), 0700)
}

func dataDir() (string, error) {
	if custom := os.Getenv("ROTAKEY_CODEX_DATA_DIR"); custom != "" {
		return filepath.Abs(custom)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "rotakey-codex"), nil
}

func codexConfigPath() (string, error) {
	if custom := os.Getenv("CODEX_HOME"); custom != "" {
		return filepath.Join(custom, "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func statePath() (string, error) {
	dir, err := dataDir()
	return filepath.Join(dir, "state.json"), err
}

func readState() (state, error) {
	var current state
	path, err := statePath()
	if err != nil {
		return current, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return current, errors.New("Rotakey is not installed; run rotakey-codex install")
	}
	err = json.Unmarshal(body, &current)
	return current, err
}

func writeState(current state) error {
	body, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	path, _ := statePath()
	return atomicWrite(path, append(body, '\n'), 0600)
}

func backupConfig(path string, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	name := path + ".rotakey-backup-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	return atomicWrite(name, body, 0600)
}

func atomicWrite(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".rotakey-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path + ".replace")
		if _, err := os.Stat(path); err == nil {
			if err := os.Rename(path, path+".replace"); err != nil {
				return err
			}
		}
		if err := os.Rename(tempPath, path); err != nil {
			_ = os.Rename(path+".replace", path)
			return err
		}
		_ = os.Remove(path + ".replace")
		return nil
	}
	return os.Rename(tempPath, path)
}

func tomlQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func client() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
}

func commandExists(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func commandOutput(name string, args ...string) string {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "unavailable"
	}
	return strings.TrimSpace(string(output))
}

func check(name string, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
