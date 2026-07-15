INSERT INTO datasets (project_id, slug, name, description, status)
SELECT id, 'community', 'Community Content', 'Default project content dataset', 'active'
FROM projects
ON CONFLICT (project_id, slug) DO NOTHING;

INSERT INTO dataset_notes (dataset_id, note_id, note_version)
SELECT d.id, n.id, n.content_version
FROM notes n
JOIN datasets d ON d.project_id = n.project_id AND d.slug = 'community' AND d.status = 'active'
ON CONFLICT (dataset_id, note_id) DO UPDATE
SET note_version = EXCLUDED.note_version;

UPDATE evidence_sources es
SET dataset_id = d.id,
    updated_at = now()
FROM datasets d
WHERE d.project_id = es.project_id
  AND d.slug = 'community'
  AND d.status = 'active'
  AND es.dataset_id IS DISTINCT FROM d.id;

DELETE FROM dataset_notes dn
USING datasets old_dataset, datasets canonical
WHERE dn.dataset_id = old_dataset.id
  AND canonical.project_id = old_dataset.project_id
  AND canonical.slug = 'community'
  AND old_dataset.id <> canonical.id;

DELETE FROM datasets old_dataset
USING datasets canonical
WHERE canonical.project_id = old_dataset.project_id
  AND canonical.slug = 'community'
  AND old_dataset.id <> canonical.id
  AND old_dataset.slug = 'community-current';
