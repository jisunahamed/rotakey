import { useEffect, useRef, useState } from "react";
import { BookOpen, ChevronDown, Github, LogOut } from "lucide-react";
import { useConfirm, useConfirmOpen } from "../ui";
import { themeChoices, type ThemeChoice } from "./theme";
import { versionState, type VersionInfo } from "./version";

/** Everything about the person signed in and the install they are signed in to,
 *  behind one control.
 *
 *  The rail's foot used to be five loose items competing with the seven navigation
 *  rows above them: an update card, two link rows, a name with a bare power icon
 *  beside it that signed out on the first click, and a version line. None of them
 *  is something an operator navigates to, and one of them destroyed a half-filled
 *  form. They are all still here — this is the menu they are in. */
export function AccountMenu({
  username,
  version,
  theme,
  setTheme,
  onSignOut
}: {
  username: string;
  version: VersionInfo | null;
  theme: ThemeChoice;
  setTheme: (theme: ThemeChoice) => void;
  onSignOut: () => Promise<void>;
}) {
  const ask = useConfirm();
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement | null>(null);
  const trigger = useRef<HTMLButtonElement | null>(null);
  // The sign-out confirmation renders above the menu and takes focus out of it.
  // While it is up the menu's Escape and outside-click handling stands down, or
  // Escape would answer the question and shut the menu in one keypress, and the
  // click that opened the dialog would be read as a click outside the menu.
  const confirmOpen = useConfirmOpen();
  const confirmOpenRef = useRef(confirmOpen);
  confirmOpenRef.current = confirmOpen;

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (confirmOpenRef.current || event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      trigger.current?.focus();
    };
    // pointerdown rather than click: a menu should be gone by the time the button
    // underneath it is pressed, not after.
    const onPointerDown = (event: PointerEvent) => {
      if (confirmOpenRef.current) return;
      if (!container.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  const release = versionState(version);
  const updateReady = Boolean(version?.update_available && version.latest_version);

  const signOut = async () => {
    const confirmed = await ask({
      title: "Sign out?",
      body: "Anything you have typed and not saved will be lost. The gateway keeps routing while you are signed out.",
      confirmLabel: "Sign out",
      cancelLabel: "Stay signed in"
    });
    if (!confirmed) return;
    setOpen(false);
    await onSignOut();
  };

  return (
    <div className="account" ref={container}>
      <button
        className={`account__trigger${open ? " is-open" : ""}`}
        onClick={() => setOpen((current) => !current)}
        aria-expanded={open}
        aria-haspopup="true"
        ref={trigger}
      >
        <span className="account__avatar" aria-hidden="true">{username.slice(0, 1).toUpperCase()}</span>
        <span className="account__who">
          <strong>{username}</strong>
          <small>Owner</small>
        </span>
        {/* An update is the one thing down here worth interrupting for, and it is
            now a click away rather than on screen. The dot is what makes it
            findable; the label is what makes it announceable. */}
        {updateReady && <span className="account__badge" role="img" aria-label="An update is available" />}
        <ChevronDown size={15} aria-hidden="true" />
      </button>

      {open && (
        <div className="account__menu" role="group" aria-label={`Account and settings for ${username}`}>
          <div className="account__section">
            <p className="account__caption" id="account-theme-label">Theme</p>
            <div className="account__themes" role="radiogroup" aria-labelledby="account-theme-label">
              {themeChoices.map((choice) => (
                <button
                  key={choice.value}
                  role="radio"
                  aria-checked={theme === choice.value}
                  className={`account__theme${theme === choice.value ? " is-active" : ""}`}
                  title={choice.description}
                  onClick={() => setTheme(choice.value)}
                >
                  {choice.label}
                </button>
              ))}
            </div>
          </div>

          <div className="account__section">
            <a
              className="account__item"
              href="https://github.com/jisunahamed/rotakey/blob/main/docs/OPERATOR-GUIDE.md"
              target="_blank"
              rel="noreferrer"
            >
              <BookOpen size={15} aria-hidden="true" />
              <span>Operator guide</span>
            </a>
            <a className="account__item" href="https://github.com/jisunahamed/rotakey" target="_blank" rel="noreferrer">
              <Github size={15} aria-hidden="true" />
              <span>Rotakey on GitHub</span>
            </a>
          </div>

          {/* The version reads as a fact plus what is known about it. When there is
              a release to go to it becomes the link to it; when there is not, it
              stays a readout rather than pretending to be actionable. */}
          <div className="account__section">
            <p className="account__caption">This install</p>
            {updateReady ? (
              <a className="account__version" href={version?.release_url} target="_blank" rel="noreferrer">
                <strong>Rotakey v{version?.current_version}</strong>
                <span className="account__version-state is-update">{release.text}</span>
                <small>{release.detail} →</small>
              </a>
            ) : (
              <div className="account__version" title={version?.commit ? `Commit ${version.commit}` : undefined}>
                <strong>{version ? `Rotakey v${version.current_version}` : "Rotakey"}</strong>
                <span className="account__version-state">{release.text}</span>
                <small>{release.detail}</small>
              </div>
            )}
          </div>

          <div className="account__section">
            <button className="account__item account__item--danger" onClick={() => void signOut()}>
              <LogOut size={15} aria-hidden="true" />
              <span>Sign out</span>
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
