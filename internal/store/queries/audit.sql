-- name: ReceivedDeliveries :many
-- The delivery GUIDs among ids that ingest recorded (delivery.received),
-- for the delivery audit's missed classification.
SELECT DISTINCT delivery_id
FROM webhook_deliveries
WHERE delivery_id = ANY(@ids::uuid[])
  AND received_at IS NOT NULL;
