import { useId, useState, type ReactNode } from "react";
import { KeyRound, Trash2 } from "lucide-react";
import {
  Button,
  Cell,
  DataTable,
  Disclosure,
  Dot,
  Empty,
  Field,
  FieldPair,
  FieldRow,
  FieldStack,
  Inspector,
  Label,
  Menu,
  MenuItem,
  MenuSection,
  Notice,
  Row,
  SearchInput,
  SectionHeader,
  Segmented,
  Stat,
  Surface,
  TabPanel,
  Tabs,
  Tag,
  Toolbar,
  Workbench,
  WorkbenchFrame,
  states,
  useListKeys,
  type ConsoleState,
  type NoticeTone
} from "../ui";

// Behind the guard rather than imported plainly at the top, because a plain CSS
// import is a side effect and Rollup will not drop one — the component below is
// tree-shaken out of a production build, but its stylesheet would still have been
// concatenated into the bundle the gateway serves. `import.meta.env.DEV` becomes a
// literal `false` at build time, so this line and the file behind it disappear.
if (import.meta.env.DEV) void import("./kitchen-sink.css");

/** Every primitive, in every variant, in every state, on one scrolling page.
 *
 *  The console has no test runner — `tsc` is the whole safety net, and it verifies
 *  nothing about layout, focus order or contrast. This page is the substitute: it
 *  is what gets looked at before and after any commit that touches the kit, at
 *  four widths and in both themes, so a change to a shared rule cannot quietly
 *  alter a surface nobody thought to open.
 *
 *  It is reachable at /admin/__ui in a dev build and nowhere else. Nothing links
 *  to it, the rail does not list it, and the search palette does not index it: in
 *  a production build the path is a 404 like any other typo, because this page is
 *  a tool for whoever is changing the kit and not a feature of the product.
 *
 *  Two rules for what goes here. Every variant a primitive offers appears, because
 *  a variant that is never looked at is a variant that is quietly broken; and each
 *  one is shown with realistic content, because a `Stat` reading "Label / 42" tells
 *  you nothing about the `Stat` that has to hold "1,284,904 tokens" in 120px. */

const allStates = Object.keys(states) as ConsoleState[];
const noticeTones: NoticeTone[] = ["info", "success", "warning", "danger"];

/** The console's theme choice, written out here rather than imported from the
 *  shell so that `dev/` depends on nothing above it. The two cannot drift: a
 *  fourth choice added to the shell fails to typecheck against `setTheme` below. */
type ThemeChoice = "light" | "dark" | "system";
const themeChoices: ThemeChoice[] = ["light", "dark", "system"];

/** Rows with the shape the console's lists actually have: a name with a state
 *  beside it, a second line nobody reads until it matters, and two figures. */
const routes = [
  { alias: "gpt-4o", upstream: "gpt-4o-2024-08-06", provider: "OpenAI production", state: "healthy", keys: "4 / 4", calls: "1,284" },
  { alias: "claude-sonnet-4-5", upstream: "claude-sonnet-4-5-20250929", provider: "Anthropic", state: "partial", keys: "2 / 3", calls: "918" },
  { alias: "gemini-2.5-pro", upstream: "gemini-2.5-pro", provider: "Google AI Studio", state: "cooldown", keys: "1 / 2", calls: "204" },
  { alias: "a-very-long-alias-that-has-nowhere-to-go", upstream: "azure-foundry/deepseek-r1-0528-with-a-long-deployment-name", provider: "Azure AI Foundry", state: "quarantined", keys: "0 / 1", calls: "0" },
  { alias: "gpt-4o-mini", upstream: "gpt-4o-mini-2024-07-18", provider: "OpenAI production", state: "unverified", keys: "4 / 4", calls: "—" }
] as const;

function Bench({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="sink-bench">
      <SectionHeader title={title} level={2} description={note} />
      <div className="sink-bench__body">{children}</div>
    </section>
  );
}

export function KitchenSink({ theme, setTheme }: { theme: ThemeChoice; setTheme: (next: ThemeChoice) => void }) {
  const tabBase = useId();
  const [selected, setSelected] = useState<string>(routes[0].alias);
  const [drawer, setDrawer] = useState(false);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("all");
  const [range, setRange] = useState("24h");
  const [tab, setTab] = useState("connection");
  const [openSection, setOpenSection] = useState("keys");
  const [naming, setNaming] = useState("model");
  const onListKeys = useListKeys();
  const current = routes.find((route) => route.alias === selected) ?? routes[0];

  return (
    <div className="sink">
      <SectionHeader
        title="Primitive kit"
        level={1}
        description="Every primitive in every variant. Development builds only — this page is not part of the product."
        meta={`${allStates.length} states`}
        actions={themeChoices.map((choice) => (
          <Button
            key={choice}
            variant={theme === choice ? "primary" : "quiet"}
            onClick={() => setTheme(choice)}
            aria-pressed={theme === choice}
          >
            {choice}
          </Button>
        ))}
      />

      <Bench title="Surface" note="Three depths: resting on the canvas, cut into it, or floating over it.">
        <div className="sink-row">
          <Surface pad="md">
            <strong>raised</strong>
            <p>A slab on the canvas. Lit along its top edge, which is the console's whole depth model.</p>
          </Surface>
          <Surface tone="inset" pad="md">
            <strong>inset</strong>
            <p>A well cut into the surface around it. No lit edge — a hole is not lit from above.</p>
          </Surface>
          <Surface tone="float" pad="md">
            <strong>float</strong>
            <p>Above the page and temporary: a menu, a popover. The only real shadow in the console.</p>
          </Surface>
        </div>
        <div className="sink-row">
          <Surface pad="sm" radius="fitting"><code>radius=fitting · pad=sm</code></Surface>
          <Surface pad="md"><code>radius=panel · pad=md</code></Surface>
          <Surface pad="lg" radius="none"><code>radius=none · pad=lg</code></Surface>
        </div>
      </Bench>

      <Bench title="Label" note="The uppercase micro-label. Accent is opt-in and should stay rare.">
        <div className="sink-row">
          <Surface pad="md"><Label>Requests per minute</Label></Surface>
          <Surface pad="md"><Label tone="accent">Needs your attention</Label></Surface>
          <Surface pad="md"><Label>A label long enough to wrap onto a second line at this width</Label></Surface>
        </div>
      </Bench>

      <Bench title="SectionHeader" note="Four levels. Size follows the level, so a heading cannot look like the wrong depth.">
        <Surface>
          <SectionHeader title="Level 1 — a page" level={1} description="What this page owns, in one sentence." meta="4 providers · 12 keys" actions={<Button variant="quiet">Refresh</Button>} />
        </Surface>
        <Surface>
          <SectionHeader title="Level 2 — a panel" level={2} description="Every request and why it went the way it did." meta="100 of 4,182" actions={<Button>Add route</Button>} />
        </Surface>
        <Surface>
          <SectionHeader title="Level 3 — a section" level={3} meta="3 keys" />
        </Surface>
        <Surface>
          <SectionHeader title="Level 4 — a group inside a form" level={4} description="Only a fact belongs in the meta slot." />
        </Surface>
        <Surface>
          <SectionHeader
            title="A title long enough that the actions have nowhere left to go on a narrow screen"
            level={2}
            description="The row wraps rather than truncating: a clipped heading costs the operator the name of the thing they are looking at."
            actions={<><Button variant="quiet">Cancel</Button><Button>Save</Button></>}
          />
        </Surface>
      </Bench>

      <Bench title="Dot and its phrases" note="One map drives the hue, the words and the sentence. A state missing from it is a typecheck failure, not a grey dot.">
        <Surface>
          <dl className="sink-states">
            {allStates.map((state) => (
              <div key={state}>
                <dt><Dot state={state} /><code>{state}</code></dt>
                <dd><strong>{states[state].phrase}</strong><span>{states[state].meaning}</span></dd>
              </div>
            ))}
          </dl>
        </Surface>
      </Bench>

      <Bench title="Tag" note="A short word or a figure in a hairline box. Figures take the data face so a column of them lines up.">
        <div className="sink-row sink-row--tight">
          <Tag>chat</Tag>
          <Tag>responses</Tag>
          <Tag>messages</Tag>
          <Tag tone="accent">Primary</Tag>
          <Tag tone="live">Verified</Tag>
          <Tag tone="hold">Not checked</Tag>
          <Tag tone="fault">Failed</Tag>
          <Tag tone="idle">Off</Tag>
          <Tag tone="busy">Checking</Tag>
        </div>
        <div className="sink-row sink-row--tight">
          <Tag figure>200</Tag>
          <Tag figure tone="live">200</Tag>
          <Tag figure tone="hold">429</Tag>
          <Tag figure tone="fault">500</Tag>
          <Tag figure tone="busy">···</Tag>
          <Tag figure>304</Tag>
          <Tag figure>18</Tag>
        </div>
      </Bench>

      <Bench title="Stat" note="A figure and the word for what it counts. Nothing here is allowed to be clipped.">
        <Surface>
          <div className="sink-stats">
            <Stat label="Requests" value="4,182" />
            <Stat label="Tokens" value="1,284,904" note="in + out" />
            <Stat label="Errors" value="37" tone="fault" note="0.9% of traffic" />
            <Stat label="Keys ready" value="11 / 14" tone="hold" />
            <Stat label="P95 latency" value="1,940 ms" />
            <Stat label="Estimated cost" value="$18.42" />
            <Stat label="Balance" value="—" note="Not tracked" tone="idle" />
            <Stat label="Gateway key" value="Ready" tone="live" />
          </div>
        </Surface>
      </Bench>

      <Bench title="Notice" note="A message in the place it is about. Danger interrupts a screen reader; everything else waits its turn.">
        {noticeTones.map((tone) => (
          <Notice key={tone} tone={tone}>
            {tone === "danger"
              ? "Rotakey could not reach Redis, so it refused the request rather than ignoring your rate limits. Every request fails until Redis is back."
              : `A ${tone} notice, in one sentence that says what happened and what it means.`}
          </Notice>
        ))}
        <Notice tone="warning" title="Two of your keys are paused" action={<Button variant="quiet">Show them</Button>}>
          An error or a rate limit made Rotakey rest them for a moment. They return to use on their own.
        </Notice>
      </Bench>

      <Bench title="Empty" note="What is missing, why, and what to do about it. An empty state with no way out is a dead end.">
        <Empty
          level={3}
          title="No requests in the last hour"
          description="Nothing has been sent through the gateway since 09:20. Widen the time range to see older traffic."
          action={<Button variant="quiet">Show the last 24 hours</Button>}
        />
        <Empty
          level={3}
          size="pane"
          title="No key selected"
          description="Choose a key on the left to see what it has served and why it is in the state it is in."
        />
      </Bench>

      <Bench title="DataTable, Row and Cell" note="The track list is written once and read by the head and by every row, so a caption cannot drift off the column under it. Arrows move, Enter opens.">
        <Surface>
          <DataTable
            columns="minmax(0, 1.7fr) minmax(0, 1fr) 64px 64px"
            label="Model routes"
            actions
            onKeyDown={onListKeys}
            head={
              <>
                <Cell>Route</Cell>
                <Cell>Provider</Cell>
                <Cell align="end">Keys</Cell>
                <Cell align="end">Calls</Cell>
              </>
            }
          >
            {routes.map((route) => (
              <Row
                key={route.alias}
                href="/admin/__ui"
                selected={route.alias === selected}
                onClick={() => setSelected(route.alias)}
                title={route.alias}
                actions={
                  <Menu label={`More actions for ${route.alias}`}>
                    <MenuSection caption="This route">
                      <MenuItem icon={<KeyRound size={14} aria-hidden="true" />}>Manage its keys</MenuItem>
                      <MenuItem>Copy the name</MenuItem>
                      <MenuItem disabled>Check it now</MenuItem>
                    </MenuSection>
                    <MenuSection>
                      <MenuItem tone="danger" icon={<Trash2 size={14} aria-hidden="true" />}>Delete this route</MenuItem>
                    </MenuSection>
                  </Menu>
                }
              >
                <Cell icon={<Dot state={route.state} label="" />} sub={route.upstream}>{route.alias}</Cell>
                <Cell sub={states[route.state].phrase}>{route.provider}</Cell>
                <Cell align="end" figure>{route.keys}</Cell>
                <Cell align="end" figure>{route.calls}</Cell>
              </Row>
            ))}
          </DataTable>
        </Surface>

        {/* The other half of the primitive: rows that open nothing. No chevron, no
            pointer, and nothing in the tab order pretending there is more to see. */}
        <Surface>
          <DataTable columns="minmax(0, 1fr) 56px" label="What went wrong today" linked={false}>
            {[
              { code: "429", text: "The provider asked Rotakey to slow down" },
              { code: "500", text: "The provider failed on its side" },
              { code: "—", text: "The reply stopped part-way through" }
            ].map((error) => (
              <Row key={error.text} disabled>
                <Cell>{error.text}</Cell>
                <Cell align="end" figure>{error.code}</Cell>
              </Row>
            ))}
          </DataTable>
        </Surface>
      </Bench>

      <Bench title="Field, FieldPair and FieldStack" note="The label wraps the control, so there is no id to get wrong. A field that can be invalid takes a function and is handed the two attributes that tie its message to it.">
        <Surface pad="md">
          <FieldStack>
            <Field label="Route name" hint="what callers ask for">
              <input type="text" defaultValue="gpt-4o" />
            </Field>
            <FieldPair>
              <Field label="Requests per minute" hint="0 for no limit">
                <input type="number" defaultValue={600} />
              </Field>
              <Field label="Tokens per minute" hint="0 for no limit">
                <input type="number" defaultValue={400000} />
              </Field>
            </FieldPair>
            <FieldPair columns={3}>
              <Field label="Input price" hint="per million">
                <input type="number" defaultValue={2.5} step={0.01} />
              </Field>
              <Field label="Output price" hint="per million">
                <input type="number" defaultValue={10} step={0.01} />
              </Field>
              <Field label="Currency">
                <select defaultValue="usd">
                  <option value="usd">US dollars</option>
                  <option value="eur">Euros</option>
                </select>
              </Field>
            </FieldPair>
            <Field
              label="Longest reply"
              hint="tokens"
              error="Must be between 1 and 128,000. Leave it empty to use whatever the provider allows."
            >
              {(control) => <input type="number" defaultValue={250000} {...control} />}
            </Field>
            <Field label="Note to yourself" span>
              <textarea rows={2} defaultValue="Kept for the billing export at the end of the month." />
            </Field>
          </FieldStack>
        </Surface>
      </Bench>

      <Bench title="FieldRow" note="The other shape a control takes: a setting, with the sentence saying what changes given the width to say it, and a control slot wide enough for the longest thing that can sit in it.">
        <Surface>
          <FieldRow
            label="Model naming"
            description="How a caller names a model. Pooled hides which provider serves it, so one name can fall back across all of them."
          >
            <select value={naming} onChange={(event) => setNaming(event.target.value)}>
              <option value="model">Model-wise (pooled)</option>
              <option value="provider">Provider-wise (one name per provider)</option>
            </select>
          </FieldRow>
          <FieldRow
            label="Wait for a rate-limited key"
            description="How long Rotakey holds a request while every key is over its limit, before it gives up and answers with an error."
            error="Must be between 0 and 60,000 milliseconds."
          >
            {(control) => <input type="number" defaultValue={90000} {...control} />}
          </FieldRow>
          <FieldRow
            label="Keep request history for"
            description="Older requests are deleted on the hour. Captured request and reply text cannot outlive this."
            wide
          >
            <Segmented
              label="Keep request history for"
              figures
              value={range}
              onChange={setRange}
              items={[
                { id: "24h", label: "24h" },
                { id: "7d", label: "7d" },
                { id: "30d", label: "30d" },
                { id: "90d", label: "90d" }
              ]}
            />
          </FieldRow>
        </Surface>
      </Bench>

      <Bench title="Toolbar and SearchInput" note="One strip above every list, at one height, with the magnifier inside the control rather than beside it where a narrow screen can drop it.">
        <Surface pad="sm">
          <Toolbar label="Filter model routes">
            <SearchInput
              value={query}
              onChange={setQuery}
              label="Filter model routes"
              placeholder="gpt-4o, anthropic, a key label…"
            />
            <Segmented
              label="State"
              value={status}
              onChange={setStatus}
              items={[
                { id: "all", label: "All", badge: 5 },
                { id: "ready", label: "Ready", badge: 2 },
                { id: "attention", label: "Need attention", badge: 3 }
              ]}
            />
            <Button variant="quiet">Check all</Button>
            <Button>Add route</Button>
          </Toolbar>
        </Surface>
      </Bench>

      <Bench title="Tabs" note="Switches which panel is on screen. Roving tabindex, arrow keys, and a panel that is hidden rather than unmounted so a half-typed value survives a look at the one next to it.">
        <Surface pad="md">
          <Tabs
            base={tabBase}
            label="Provider details"
            value={tab}
            onChange={setTab}
            items={[
              { id: "connection", label: "Connection" },
              { id: "keys", label: "API keys", badge: 3 },
              { id: "routes", label: "Model routes", badge: 12 }
            ]}
          />
          <TabPanel base={tabBase} id="connection" active={tab === "connection"}>
            <FieldStack>
              <Field label="Address" hint="where requests are sent">
                <input type="url" defaultValue="https://api.openai.com/v1" />
              </Field>
            </FieldStack>
          </TabPanel>
          <TabPanel base={tabBase} id="keys" active={tab === "keys"}>
            <Notice tone="info">Three keys, all ready. Rotakey moves to the next one whenever a key is resting.</Notice>
          </TabPanel>
          <TabPanel base={tabBase} id="routes" active={tab === "routes"}>
            <Empty
              level={3}
              title="Routes are edited on the Models page"
              description="This tab lists what points at this provider. Adding and changing a route happens in one place, so two editors cannot disagree about it."
              action={<Button variant="quiet">Go to Models</Button>}
            />
          </TabPanel>
        </Surface>
      </Bench>

      <Bench title="Segmented" note="Picks one value out of a few and changes nothing about the layout. It is a radio group and not a set of tabs — the difference is what a screen reader is told, and it was wrong on all five of these.">
        <div className="sink-row sink-row--tight">
          <Segmented
            label="Time range"
            figures
            value={range}
            onChange={setRange}
            items={[
              { id: "24h", label: "24h" },
              { id: "7d", label: "7d" },
              { id: "30d", label: "30d" },
              { id: "90d", label: "90d" }
            ]}
          />
          <Segmented
            label="Status"
            value={status}
            onChange={setStatus}
            items={[
              { id: "all", label: "All" },
              { id: "ready", label: "Ready" },
              { id: "attention", label: "Need attention" }
            ]}
          />
        </div>
      </Bench>

      <Bench title="Disclosure" note="A real heading with the button inside it, so the section can be found by heading and still opens on Enter. Its actions sit outside the toggle, and its body is hidden rather than unmounted.">
        <Surface>
          <Disclosure
            level={3}
            title="API keys"
            subtitle="What Rotakey signs requests with"
            meta={<Tag figure>3</Tag>}
            open={openSection === "keys"}
            onToggle={() => setOpenSection(openSection === "keys" ? "" : "keys")}
            actions={<Button variant="quiet">Add a key</Button>}
          >
            <DataTable columns="minmax(0, 1fr) 96px" label="API keys on this provider" linked={false}>
              {[
                { label: "Primary", state: "healthy", calls: "1,284" },
                { label: "Overflow", state: "cooldown", calls: "204" },
                { label: "Old billing account", state: "exhausted", calls: "0" }
              ].map((key) => (
                <Row key={key.label}>
                  <Cell icon={<Dot state={key.state as ConsoleState} label="" />} sub={states[key.state as ConsoleState].phrase}>
                    {key.label}
                  </Cell>
                  <Cell align="end" figure>{key.calls}</Cell>
                </Row>
              ))}
            </DataTable>
          </Disclosure>
          <Disclosure
            level={3}
            title="Model routes"
            subtitle="What callers can ask this provider for"
            meta={<Tag figure>12</Tag>}
            open={openSection === "routes"}
            onToggle={() => setOpenSection(openSection === "routes" ? "" : "routes")}
          >
            <Notice tone="info">Twelve routes point at this provider. They are edited on the Models page.</Notice>
          </Disclosure>
        </Surface>
      </Bench>

      <Bench title="Menu" note="Every option survives and sits somewhere it can be found by clicking. Escape closes it back onto its trigger, a press outside closes it before the thing underneath is pressed, and the arrow keys walk it.">
        <div className="sink-row sink-row--tight">
          <Menu label="More actions for gpt-4o">
            <MenuSection caption="This route">
              <MenuItem icon={<KeyRound size={14} aria-hidden="true" />}>Manage its keys</MenuItem>
              <MenuItem>Copy the name</MenuItem>
              <MenuItem disabled>Check it now</MenuItem>
            </MenuSection>
            <MenuSection>
              <MenuItem tone="danger" icon={<Trash2 size={14} aria-hidden="true" />}>Delete this route</MenuItem>
            </MenuSection>
          </Menu>
          <Menu label="Account and appearance" align="start" trigger="Signed in as owner">
            <MenuSection caption="Appearance">
              <MenuItem>Light</MenuItem>
              <MenuItem>Dark</MenuItem>
              <MenuItem>Match my system</MenuItem>
            </MenuSection>
            <MenuSection caption="Help">
              <MenuItem href="https://github.com/rotakey/rotakey">Operator guide</MenuItem>
            </MenuSection>
            <MenuSection>
              <MenuItem tone="danger">Sign out</MenuItem>
            </MenuSection>
          </Menu>
        </div>
      </Bench>

      <Bench title="Workbench and Inspector" note="A list beside whatever is open, each column scrolling on its own. Below 900px the right-hand column stops being a column and becomes a drawer over the list — click a row to see it.">
        <WorkbenchFrame>
          <SectionHeader
            title="Model routes"
            level={2}
            description="Every name a caller can ask for, and where Rotakey sends it."
            meta="5 routes · 11 keys"
            actions={<Button>Add route</Button>}
          />
          <Workbench
            inspectorOpen={drawer}
            list={
              <>
                <Toolbar label="Filter model routes">
                  <SearchInput value={query} onChange={setQuery} label="Filter model routes" placeholder="gpt-4o, anthropic…" />
                </Toolbar>
                <DataTable
                  columns="minmax(0, 1fr) 64px"
                  label="Model routes"
                  onKeyDown={onListKeys}
                  head={<><Cell>Route</Cell><Cell align="end">Calls</Cell></>}
                >
                  {routes.map((route) => (
                    <Row
                      key={route.alias}
                      selected={route.alias === selected}
                      title={route.alias}
                      onClick={() => {
                        setSelected(route.alias);
                        setDrawer(true);
                      }}
                    >
                      <Cell icon={<Dot state={route.state} label="" />} sub={route.provider}>{route.alias}</Cell>
                      <Cell align="end" figure>{route.calls}</Cell>
                    </Row>
                  ))}
                </DataTable>
              </>
            }
            inspector={
              <Inspector
                title={current.alias}
                level={3}
                subtitle={current.upstream}
                onClose={() => setDrawer(false)}
                meta={
                  <>
                    <Dot state={current.state} label="" />
                    <span>{states[current.state].phrase}</span>
                    <Tag>chat</Tag>
                    <Tag>responses</Tag>
                  </>
                }
                actions={
                  <Menu label={`More actions for ${current.alias}`}>
                    <MenuSection>
                      <MenuItem icon={<KeyRound size={14} aria-hidden="true" />}>Manage its keys</MenuItem>
                      <MenuItem tone="danger" icon={<Trash2 size={14} aria-hidden="true" />}>Delete this route</MenuItem>
                    </MenuSection>
                  </Menu>
                }
              >
                <Notice tone="info">{states[current.state].meaning}</Notice>
                <div className="sink-stats">
                  <Stat label="Keys ready" value={current.keys} />
                  <Stat label="Calls today" value={current.calls} />
                  <Stat label="Tokens" value="184,220" note="in + out" />
                  <Stat label="Errors" value="2" tone="fault" />
                </div>
                <FieldStack>
                  <Field label="Route name" hint="what callers ask for">
                    <input type="text" defaultValue={current.alias} />
                  </Field>
                  <Field label="Model at the provider">
                    <input type="text" defaultValue={current.upstream} />
                  </Field>
                  <FieldPair>
                    <Field label="Requests per minute" hint="0 for no limit">
                      <input type="number" defaultValue={600} />
                    </Field>
                    <Field label="Tokens per minute" hint="0 for no limit">
                      <input type="number" defaultValue={400000} />
                    </Field>
                  </FieldPair>
                </FieldStack>
              </Inspector>
            }
          />
        </WorkbenchFrame>
      </Bench>
    </div>
  );
}
