import { Link, useParams } from "react-router-dom";
import { PageHeading, Notice } from "../components/ui";
export default function RunDetails() {
  const { id } = useParams();
  return (
    <>
      <PageHeading
        title="Run details"
        breadcrumb={
          <>
            Run History / <code>{id}</code>
          </>
        }
        description="Run records will be available with Phase 3 execution support."
      />
      <Notice tone="warning" title="Unavailable until Phase 3">
        This application has not executed this migration and has no run details
        to display.
      </Notice>
      <Link className="button secondary" to="/runs">
        Back to Run History
      </Link>
    </>
  );
}
