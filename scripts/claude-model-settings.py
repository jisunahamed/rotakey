#!/usr/bin/env python3
"""Generate a Claude --settings file without changing user settings or keys."""

import argparse
import json
import os
from pathlib import Path
from urllib.parse import urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def model_settings(models, alias, details):
    selected = next((m for m in models if m.get("id") == alias), None)
    if selected is None:
        raise ValueError("The selected alias is not in this gateway's model list")
    levels = selected.get("reasoning_levels", [])
    capabilities = []
    if levels:
        capabilities.append("effort")
    if "max" in levels:
        capabilities.append("max_effort")
    if "xhigh" in levels:
        capabilities.append("xhigh_effort")
    thinking = details.get("capabilities", {}).get("thinking", {})
    types = thinking.get("types", {})
    if thinking.get("supported") and types.get("enabled", {}).get("supported"):
        capabilities.append("thinking")
    if thinking.get("supported") and types.get("adaptive", {}).get("supported"):
        capabilities.append("adaptive_thinking")
    env = {
        "ANTHROPIC_CUSTOM_MODEL_OPTION": alias,
        "ANTHROPIC_CUSTOM_MODEL_OPTION_NAME": alias,
        "ANTHROPIC_CUSTOM_MODEL_OPTION_SUPPORTED_CAPABILITIES": ",".join(capabilities),
    }
    window = selected.get("context_window")
    if isinstance(window, int) and window > 0:
        env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = str(window)
    # Explicit picker rows prevent Claude from adding a second [1m] row.
    # All public aliases remain selectable; no other model is impersonated.
    options = []
    seen = set()
    for model in models:
        name = model.get("id")
        if isinstance(name, str) and name and name not in seen:
            seen.add(name)
            options.append({"model": name, "label": name})
    return {"model": alias, "env": env,
            "modelPicker": {"replaceBuiltInOptions": True, "options": options}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, help="Rotakey public model alias")
    parser.add_argument("--base-url", default=os.environ.get("ANTHROPIC_BASE_URL"))
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    base = (args.base_url or "").rstrip("/")
    parsed = urlsplit(base)
    if parsed.scheme != "https" or not parsed.netloc or parsed.username is not None or parsed.query or parsed.fragment:
        parser.error("--base-url must be an HTTPS gateway URL")
    token = os.environ.get("ANTHROPIC_AUTH_TOKEN") or os.environ.get("ANTHROPIC_API_KEY")
    if not token:
        parser.error("Set ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY first")
    opener = build_opener(NoRedirect())

    def fetch(anthropic=False):
        headers = {"Authorization": "Bearer " + token}
        if anthropic:
            headers["anthropic-version"] = "2023-06-01"
        with opener.open(Request(base + "/v1/models", headers=headers), timeout=30) as reply:
            return json.load(reply)["data"]

    try:
        models = fetch()
        details = next((m for m in fetch(True) if m.get("display_name") == args.model), {})
        settings = model_settings(models, args.model, details)
        settings["env"]["ANTHROPIC_BASE_URL"] = base
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(settings, indent=2) + "\n", encoding="utf-8")
    except Exception as error:
        parser.exit(1, f"Could not create Claude model settings ({type(error).__name__}).\n")
    print(f"Settings saved to {args.output}. No credentials were written.")
    print("Effort/context overrides apply to the selected startup model. Regenerate when changing models.")
    print("Claude's effort slider can offer intermediate levels the upstream does not support; use the verified levels:",
          ", ".join(next(m for m in models if m.get("id") == args.model).get("reasoning_levels", [])) or "none")


if __name__ == "__main__":
    main()
