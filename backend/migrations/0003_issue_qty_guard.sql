CREATE OR REPLACE FUNCTION enforce_issue_qty() RETURNS trigger AS $$
DECLARE
    received integer;
BEGIN
    IF NEW.sku_id <> OLD.sku_id OR NEW.party_id <> OLD.party_id THEN
        RAISE EXCEPTION 'jobwork_issues.sku_id and party_id are immutable' USING ERRCODE = 'restrict_violation';
    END IF;
    SELECT COALESCE(SUM(qty), 0) INTO received FROM jobwork_receipts WHERE issue_id = NEW.id;
    IF NEW.qty < received THEN
        RAISE EXCEPTION 'issue qty % is below total received %', NEW.qty, received
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_enforce_issue_qty BEFORE UPDATE ON jobwork_issues
    FOR EACH ROW EXECUTE FUNCTION enforce_issue_qty();
