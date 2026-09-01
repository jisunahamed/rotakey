/** What build this is, and whether there is a newer one. Polled hourly by the
 *  shell and read only by the account menu. */

export type VersionInfo = {
  current_version: string;
  commit: string;
  build_time: string;
  latest_version?: string;
  update_available: boolean;
  release_url: string;
  published_at?: string;
  /** Why the release check could not answer. The server sends this instead of a
   * latest version when GitHub is unreachable, rate-limits it, or has published
   * nothing to compare against. */
  check_error?: string;
};

/** What the release check knows, in a sentence. The badge used to state two things
 *  it did not know: the number fell back to a hardcoded "0.2.7" whenever the
 *  request had not landed — three releases stale the day it was written — and it
 *  read "up to date" unconditionally, including when the check had failed or had
 *  never run. Every branch below is something the payload actually says. */
export function versionState(version: VersionInfo | null) {
  if (!version) return { text: "Checking…", detail: "Reading this install's version." };
  if (version.update_available && version.latest_version) {
    return { text: `v${version.latest_version} available`, detail: "View the release notes" };
  }
  if (version.check_error) return { text: "Check failed", detail: version.check_error };
  if (version.latest_version) return { text: "Up to date", detail: `v${version.latest_version} is the newest release.` };
  return { text: "No release found", detail: "There is no published release to compare this build against." };
}
