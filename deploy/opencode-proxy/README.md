# Private OpenCode proxy

Adapted from https://github.com/bigdata2211it-web/opencode-free-proxy at commit `49488fd2c6623cbcfdf11ef584722563df08aceb`, advertised under MIT. The original's Zen headers and free-model catalog are retained. This deployment uses Node's built-in HTTP transport, preserves all request parameters and upstream status/body bytes, propagates cancellation and does not print credentials. Public Anthropic/Responses conversion remains in Rotakey.

The sidecar is accessible only on the Docker network. Set `OPENCODE_PROXY_URL=http://opencode-proxy:6446` and the same private `OPENCODE_PROXY_KEY` on Rotakey and the sidecar. A provider configured with `https://opencode.ai/zen/v1` is automatically routed through it, including catalog discovery and validation. Use the normal provider/model/key editor; `public` can be the free-tier credential. Unrelated providers keep their routes. Zen provider validation fails closed if the proxy is absent.

The upstream project implements free-tier access only. This integration does not proxy other OpenCode hosts, paid gateway products or arbitrary URLs. The catalog reflects the pinned upstream repository and individual free models can be unavailable.
