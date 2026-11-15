# Verified NVIDIA model capabilities

Source: official NVIDIA Build pages. Exact integer limits are extracted from the embedded specifications; no rounded display labels are converted back into token counts. These are documented model limits, not a load test of the hosted endpoint.

| Model | Context tokens | Images | Tools | Reasoning | Structured output | Source |
|---|---:|---|---|---|---|---|
| deepseek-ai/deepseek-v4.1-flash | 1,048,576 | Yes | Yes | Yes | Unknown | [NVIDIA](https://build.nvidia.com/deepseek-ai/deepseek-v4.1-flash) |
| google/diffusiongemma-26b-a4b-it | 262,144 | Yes | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/google/diffusiongemma-26b-a4b-it) |
| google/gemma-4-31b-it | 262,144 | Yes | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/google/gemma-4-31b-it) |
| meta/muse-glimmer-30b | 131,072 | Yes | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/meta/muse-glimmer-30b) |
| minimaxai/minimax-m3.1-flash | 262,144 | Yes | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/minimaxai/minimax-m3.1-flash) |
| mistralai/mistral-large-4-1t-a52b | 262,144 | Yes | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/mistralai/mistral-large-4-1t-a52b) |
| nvidia/ising-calibration-1.5-31b | 262,144 | Yes | No | No | No | [NVIDIA](https://build.nvidia.com/nvidia/ising-calibration-1.5-31b) |
| nvidia/nemotron-3-nano-omni-30b-a3b-reasoning | 262,144 | Yes | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/nvidia/nemotron-3-nano-omni-30b-a3b-reasoning) |
| nvidia/nemotron-3-ultra-550b-a55b | 1,048,576 | No | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/nvidia/nemotron-3-ultra-550b-a55b) |
| nvidia/nemotron-3.5-bolt-e4b | 65,536 | No | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/nvidia/nemotron-3.5-bolt-e4b) |
| nvidia/nemotron-3.5-lightning-30b-a3b | 1,048,576 | No | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/nvidia/nemotron-3.5-lightning-30b-a3b) |
| openai/gpt-oss-20b | 131,072 | No | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/openai/gpt-oss-20b) |
| poolside/laguna-xs-2.1 | 262,144 | No | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/poolside/laguna-xs-2.1) |
| stepfun-ai/step-5-preview | 1,048,576 | Yes | Yes | Yes | No | [NVIDIA](https://build.nvidia.com/stepfun-ai/step-5-preview) |
| z-ai/glm-5.3-flash | 1,048,576 | Yes | Yes | Yes | Yes | [NVIDIA](https://build.nvidia.com/z-ai/glm-5-3-flash) |

## Reasoning controls

The gateway maps explicit effort between Chat `reasoning_effort`, Responses `reasoning.effort`, and Messages `output_config.effort` for each routing attempt. Native payloads and explicit provider template options retain their other fields. Existing learned/provider parameter exclusions remain effective.

NVIDIA documents low/medium/high effort for [GPT OSS 20B](https://docs.api.nvidia.com/nim/reference/openai-gpt-oss-20b) and [Mistral Large 4](https://docs.api.nvidia.com/nim/reference/mistralai-mistral-large-4-1t-a52b). A generic reasoning capability does not prove support for effort levels; xhigh/max are not invented. The reviewed Nemotron routes expose the documented `chat_template_kwargs.enable_thinking` switch across public protocols. A fixed thinking budget is not the same as this switch and is not synthesized from arbitrary effort labels.

Anthropic discovery publishes `max_input_tokens` and nested supported capability objects; Chat discovery retains its capability list and also publishes verified effort levels. Playground and the Codex manifest use the same per-model levels. Client-specific pickers can impose their own restrictions; successful discovery does not prove that Claude Desktop will expose a selector for every custom model.

Fallback catalog facts apply only to exact model IDs on official NVIDIA inference hosts. Existing explicit context values remain intact. Unknown or ambiguous models (for example agent, glm, deepseek) are not assigned a guessed context window. New matching routes use the same facts, and startup refresh is conditional to avoid overwriting concurrent operator edits. No provider/key/alias limits or pricing change.
