import { Labels, SectionTitle } from '../../components';

// AnnotationsCard renders the entity's annotations map (operator-owned
// metadata) as a chip list. Same shape as LabelsCard but distinct so a
// future change to annotation rendering (e.g. JSON expansion of
// longue-vue.io/eol.* annotations) can be made in one place.

export function AnnotationsCard({
  annotations,
}: {
  annotations?: Record<string, string> | null;
}) {
  return (
    <>
      <SectionTitle>Annotations</SectionTitle>
      <Labels labels={annotations} />
    </>
  );
}
