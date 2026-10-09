CREATE TABLE skus (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE,
    name text NOT NULL,
    image_url text NOT NULL DEFAULT ''
);

CREATE TABLE parties (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL,
    phone text NOT NULL DEFAULT ''
);

CREATE TABLE jobwork_issues (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    party_id uuid NOT NULL REFERENCES parties(id),
    sku_id uuid NOT NULL REFERENCES skus(id),
    qty integer NOT NULL CHECK (qty > 0),
    unit_rate_paise bigint NOT NULL CHECK (unit_rate_paise >= 0),
    issued_on date NOT NULL
);
CREATE INDEX idx_issues_party ON jobwork_issues(party_id);
CREATE INDEX idx_issues_sku ON jobwork_issues(sku_id);

CREATE TABLE jobwork_receipts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id uuid NOT NULL REFERENCES jobwork_issues(id),
    qty integer NOT NULL CHECK (qty > 0),
    received_on date NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_receipts_issue ON jobwork_receipts(issue_id);

CREATE TABLE vouchers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    type text NOT NULL,
    source_id uuid NOT NULL,
    amount_paise bigint NOT NULL CHECK (amount_paise >= 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','pushed','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    attempt_baseline integer NOT NULL DEFAULT 0 CHECK (attempt_baseline >= 0),
    last_error text,
    client_ref text NOT NULL UNIQUE,
    external_ref text,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until timestamptz,
    claim_token uuid,
    claimed_by text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (type, source_id)
);
CREATE INDEX idx_vouchers_due ON vouchers(next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_vouchers_status ON vouchers(status);

CREATE TABLE idempotency_requests (
    key text PRIMARY KEY,
    fingerprint text NOT NULL,
    response_status integer,
    response_body text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE accounting_records (
    client_ref text PRIMARY KEY,
    external_ref text NOT NULL UNIQUE,
    type text NOT NULL,
    amount_paise bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION enforce_receipt_qty() RETURNS trigger AS $$
DECLARE
    issued integer;
    received integer;
BEGIN
    SELECT qty INTO issued FROM jobwork_issues WHERE id = NEW.issue_id FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'issue not found: %', NEW.issue_id USING ERRCODE = 'foreign_key_violation';
    END IF;
    SELECT COALESCE(SUM(qty), 0) INTO received FROM jobwork_receipts WHERE issue_id = NEW.issue_id;
    IF received + NEW.qty > issued THEN
        RAISE EXCEPTION 'receipt qty % exceeds outstanding % for issue %', NEW.qty, issued - received, NEW.issue_id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_receipt_qty BEFORE INSERT ON jobwork_receipts
    FOR EACH ROW EXECUTE FUNCTION enforce_receipt_qty();

CREATE OR REPLACE FUNCTION forbid_receipt_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'jobwork_receipts are immutable' USING ERRCODE = 'restrict_violation';
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_forbid_receipt_mutation BEFORE UPDATE OR DELETE ON jobwork_receipts
    FOR EACH ROW EXECUTE FUNCTION forbid_receipt_mutation();
