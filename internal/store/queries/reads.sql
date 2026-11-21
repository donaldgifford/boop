-- name: ReceivedDeliveries :many
-- The delivery GUIDs among ids that ingest recorded (delivery.received),
-- for the delivery audit's missed classification.
SELECT DISTINCT delivery_id
FROM webhook_deliveries
WHERE delivery_id = ANY(@ids::uuid[])
  AND received_at IS NOT NULL;

-- name: OpenPullRequests :many
-- The PRs the store holds open in one installation's repositories, for
-- the reconcile to find the ones that left the open set.
SELECT p.repo_id, p.number
FROM pull_requests p
JOIN repositories r ON r.id = p.repo_id
WHERE p.state = 'open'
  AND r.installation_id = @installation_id
ORDER BY p.repo_id, p.number;
