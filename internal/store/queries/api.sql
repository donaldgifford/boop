-- The API role's reads (DESIGN-0004 § The API). Every query is scoped:
-- `(@all_owners::boolean OR lower(owner) = ANY(@owners::text[]))`.

-- name: VisibleOwners :many
-- The installation accounts in scope that the store knows, for /me.
SELECT DISTINCT lower(account_login)::text AS owner
FROM installations
WHERE removed_at IS NULL
  AND (@all_owners::boolean OR lower(account_login) = ANY(@owners::text[]))
ORDER BY owner;
