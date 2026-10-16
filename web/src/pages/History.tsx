import { PageHeading, Notice } from "../components/ui";
export default function History() {
  return (
    <>
      <PageHeading
        title="Run History"
        description="Historical migration results will be available with Phase 3 execution support."
        breadcrumb="Operations / Run History"
      />
      <Notice tone="warning" title="Unavailable until Phase 3">
        Migration execution and run history have not been implemented. No
        migration results are available.
      </Notice>
    </>
  );
}
