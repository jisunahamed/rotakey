import { Sheet } from "./components";
import { isMac, shortcutGroups } from "./keyboard";

/** What `?` opens.
 *
 *  A keyboard shortcut nobody can discover is a keyboard shortcut nobody has. The
 *  palette's footer names this sheet, the sheet names everything else, and `?` is
 *  where every application has put this list for thirty years.
 *
 *  It reuses `Sheet` rather than drawing a panel of its own, so it inherits the Tab
 *  trap, Escape, and focus restoration already proven by the edit forms — the
 *  behaviour a list of keyboard shortcuts is least able to get away with faking. */
export function ShortcutSheet({ onClose }: { onClose: () => void }) {
  return (
    <Sheet title="Keyboard shortcuts" onClose={onClose}>
      <div className="shortcut-groups">
        {shortcutGroups.map((group) => (
          <section className="shortcut-group" key={group.title}>
            <h3>{group.title}</h3>
            <dl>
              {group.shortcuts.map((shortcut) => (
                <div className="shortcut" key={shortcut.description}>
                  <dt>
                    {shortcut.keys.map((key) => (
                      <kbd key={key}>{key === "Ctrl" && isMac ? "⌘" : key}</kbd>
                    ))}
                  </dt>
                  <dd>{shortcut.description}</dd>
                </div>
              ))}
            </dl>
          </section>
        ))}
      </div>
    </Sheet>
  );
}
