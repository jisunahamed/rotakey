/** The frame around a page, and the states where there is no page to frame.
 *
 *  Everything here belongs to the console rather than to any one screen: the
 *  account menu at the foot of the rail, the theme choice and the release check it
 *  reads, the header a page opens with, the bars it shows while it loads, the
 *  screen shown while the console works out who is signed in, the one shown when an
 *  address names nothing, and the one-time panel a freshly-minted gateway key
 *  appears in.
 *
 *  `App.tsx` still owns the shell's own markup and state — the rail, the
 *  workspace, the message dock, the phase. Those are not a component yet, and
 *  wrapping them in one is a rewrite rather than a move. */

export { AccountMenu } from "./AccountMenu";
export { LoadingScreen } from "./LoadingScreen";
export { NotFoundPage } from "./NotFoundPage";
export { PageHeader } from "./PageHeader";
export { PageSkeleton } from "./PageSkeleton";
export { SecretReveal } from "./SecretReveal";
export { themeChoices, type ThemeChoice } from "./theme";
export { versionState, type VersionInfo } from "./version";
