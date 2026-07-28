# Rotakey design contract

| Field | Decision |
| --- | --- |
| Screen job | Show whether the gateway can serve the next model request, then let the owner fix or configure the exact provider, model route, credential, or limit responsible. |
| Primary user and action | A single developer-operator configures provider pools and copies one gateway base URL/key for every application. |
| Content hierarchy | Live model capacity first, provider/model/credential configuration second, request evidence third. |
| Navigation and controls | Compact rail navigation; dense resource table in the main pane; a contextual inspector for edits. Provider onboarding is an ordered base URL → models → credentials → test flow. |
| Visual language | IBM Plex Sans for interface copy, IBM Plex Mono for aliases/limits/logs. Cloud `#F4F6F8`, paper `#FFFFFF`, ink `#18202A`, relay blue `#147D92`, capacity amber `#C57A15`, fault red `#C2413A`, carbon `#10161C`. Tight 4/8px rhythm, quiet borders, no decorative gradients. |
| Signature | A capacity rail represents each public model as a track of credential segments, showing routing order and operational health without exposing secrets. |
| Required states | Setup, signed out, loading, no providers, missing models, missing credentials, healthy, partial capacity, exhausted, quarantined, validation error, save success, session expired, and destructive confirmation. |
| Responsive behavior | Three regions on desktop; inspector becomes an overlay below 1100px; mobile keeps capacity and logs readable and uses full-screen edit sheets. Keyboard focus is always visible and reduced-motion disables rail transitions. |
| Evidence used | Perplexity API billing: time-filtered usage hierarchy; Supabase create flow: focused guided setup; Plane resource views: dense navigation and resource inspection. Structure only—no branding, assets, or proprietary copy. |
| Forbidden defaults | Interchangeable KPI card grids, glowing gradients, filler charts, excessive pills, fake activity, vague “Manage” actions, and inert controls. |
| Acceptance criteria | Every visible control has a server outcome; all listed states render; 360/768/1440px layouts work; keyboard navigation, contrast, reduced motion, and 200% zoom remain usable. |

## Layout sketch

```text
Desktop
┌────────────┬────────────────────────────────────┬───────────────────┐
│ MODEL      │ Gateway / model capacity           │ Route inspector   │
│ RELAY      │ ═══●══════○══════●══ capacity rail │ provider          │
│            │                                    │ upstream model    │
│ Overview   │ Dense route or provider table      │ credentials       │
│ Providers  │                                    │ seven limits      │
│ Models     │ Recent request evidence            │ save/test action  │
│ Logs       │                                    │                   │
└────────────┴────────────────────────────────────┴───────────────────┘

Mobile
┌────────────────────────────┐
│ Rotakey           menu     │
│ capacity rail             │
│ route/log table           │
│                            │
│ full-screen edit sheet →  │
└────────────────────────────┘
```

The capacity rail is the single expressive element. Everything around it stays restrained and operational.
