-- +goose Up
-- Expose the external load-balancer address on Services and Ingresses.
-- Kubernetes writes `status.loadBalancer.ingress[]` whenever something
-- fulfills the Service / Ingress — cloud controllers (ELB, GCLB) on
-- managed clusters; MetalLB, Kube-VIP, or a hardware LB on-prem.
-- Preserving the K8s shape (array of {ip?, hostname?, ports?}) lets
-- readers tell apart IP-VIP setups from DNS-fronted cloud ones.
ALTER TABLE ingresses
    ADD COLUMN load_balancer JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE services
    ADD COLUMN load_balancer JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +goose Down
ALTER TABLE services   DROP COLUMN IF EXISTS load_balancer;
ALTER TABLE ingresses  DROP COLUMN IF EXISTS load_balancer;
