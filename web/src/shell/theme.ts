/** Which palette the console draws in, and the words for the three answers.
 *
 *  The choice is a preference, not a resolution: "match my system" is a third
 *  state, not shorthand for whichever of the other two is true this evening. The
 *  shell resolves it against a media query and writes the result to
 *  `document.documentElement`; everything else in the console reads the resolved
 *  attribute and never this. */

export type ThemeChoice = "light" | "dark" | "system";

/** Three choices, each with the word for it. The rail used to carry one unlabelled
 *  34px button that cycled through all three, with a heartbeat icon standing for
 *  "match my system" — so the only way to find out what any state was called was to
 *  press it and read the tooltip that told you what the *next* one was. */
export const themeChoices: readonly { value: ThemeChoice; label: string; description: string }[] = [
  { value: "light", label: "Light", description: "Always the light palette" },
  { value: "dark", label: "Dark", description: "Always the dark palette" },
  { value: "system", label: "System", description: "Follow this device's setting" }
];
