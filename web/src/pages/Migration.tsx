import { PageHeading, Notice } from "../components/ui";
function UnavailableMigration() {
  return (
    <>
      <PageHeading
        title="Run migration"
        description="Migration execution is planned for Phase 3."
        breadcrumb="Operations / Run migration"
      />
      <Notice tone="warning" title="Unavailable until Phase 3">
        No bundle upload or Flyway command is available. This console does not
        fabricate execution state.
      </Notice>
    </>
  );
}
export default UnavailableMigration;
export function Review() {
  return <UnavailableMigration />;
}
