import type { ReactNode } from "react";
import { Button } from "../Button";
import { Dot, Empty, Label, Notice, SectionHeader, Stat, Surface, Tag, states, type ConsoleState, type NoticeTone } from "../ui";

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

function Bench({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section className="sink-bench">
      <SectionHeader title={title} level={2} description={note} />
      <div className="sink-bench__body">{children}</div>
    </section>
  );
}

export function KitchenSink({ theme, setTheme }: { theme: ThemeChoice; setTheme: (next: ThemeChoice) => void }) {
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
    </div>
  );
}
