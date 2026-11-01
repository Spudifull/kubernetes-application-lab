-- Invoked only by explicit dependency-metrics-prepare, not application migrations.
-- labctl supplies metrics_password on stdin; psql output is never recorded.
BEGIN;
DO $role$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='lab_metrics') THEN
    CREATE ROLE lab_metrics LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE
      NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 2;
    COMMENT ON ROLE lab_metrics IS 'lab-dependency-metrics-v1';
  ELSIF shobj_description((SELECT oid FROM pg_roles WHERE rolname='lab_metrics'), 'pg_authid')
      IS DISTINCT FROM 'lab-dependency-metrics-v1' THEN
    RAISE EXCEPTION 'refusing an existing unrelated monitoring role';
  END IF;
  IF EXISTS (SELECT FROM pg_roles WHERE rolname='lab_metrics'
      AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls OR NOT rolcanlogin))
    OR EXISTS (SELECT FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid
      WHERE m.member=(SELECT oid FROM pg_roles WHERE rolname='lab_metrics')
        AND (r.rolname <> 'pg_monitor' OR m.admin_option)) THEN
    RAISE EXCEPTION 'monitoring role has unexpected privileges';
  END IF;
END
$role$;
GRANT pg_monitor TO lab_metrics;
GRANT CONNECT ON DATABASE lab TO lab_metrics;
ALTER ROLE lab_metrics CONNECTION LIMIT 2;
ALTER ROLE lab_metrics SET default_transaction_read_only = on;
ALTER ROLE lab_metrics SET statement_timeout = '3s';
ALTER ROLE lab_metrics SET lock_timeout = '1s';
SELECT format('ALTER ROLE lab_metrics PASSWORD %L', :'metrics_password') \gexec
-- Check effective rights, including PUBLIC grants and inherited privileges.
DO $rights$
DECLARE t regclass;
BEGIN
  FOR t IN SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
    WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f') LOOP
    IF has_table_privilege('lab_metrics', t, 'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') THEN
      RAISE EXCEPTION 'monitoring role can access application tables';
    END IF;
  END LOOP;
  IF has_database_privilege('lab_metrics', 'lab', 'CREATE')
    OR has_schema_privilege('lab_metrics', 'public', 'CREATE') THEN
    RAISE EXCEPTION 'monitoring role can create application objects';
  END IF;
END
$rights$;
COMMIT;
