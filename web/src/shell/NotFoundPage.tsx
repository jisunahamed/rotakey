import { ArrowRight } from "lucide-react";
import { Button, EmptyState } from "../ui";
import type { Page } from "../routes";

/** /admin/typo used to render the Overview, so a wrong link looked like a working
 *  one and the operator went looking for the section they thought they had opened.
 *  The path is quoted back because a mistyped or truncated address is usually
 *  obvious the moment it is read aloud. */
export function NotFoundPage({ navigate }: { navigate: (page: Page, query?: Record<string, string>) => void }) {
  return (
    <div className="resource-page">
      <header className="page-header">
        <div>
          <h1>Page not found</h1>
          <p>Rotakey has no page at <code>{location.pathname}</code>.</p>
        </div>
      </header>
      <EmptyState
        level={2}
        title="This address does not name anything"
        description="The link may be from an older version of Rotakey, or a character may be missing from it. Every page Rotakey has is listed in the navigation."
        action={<Button onClick={() => navigate("overview")}><ArrowRight size={14} aria-hidden="true" /> Go to Overview</Button>}
      />
    </div>
  );
}
