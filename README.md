CloudSQL-migrator
=================

Tools to migrate between CloudSQL instances in the nais platform.

## How does it work

Three phases: Setup, Promotion, Cleanup

## Phase 1: Setup

1. Take an explicit backup before starting
2. Set up a new SQLInstance that is the way you want it to be
   - Must create sqlinstance, sqluser and sqlsslcert resources for the new instance, with the correct owner reference
   - Do not create a sql database, because it will be replicated from the old db
3. Configure source instance for migration:
   - Set flags on the instance:
     - cloudsql.logical_decoding=on
     - cloudsql.enable_pglogical=on
   - Install the pglogical extension and grant rights to the postgres user in all databases:
    
     ```sql
     CREATE EXTENSION IF NOT EXISTS pglogical;
     GRANT USAGE on SCHEMA pglogical to "postgres";
     GRANT SELECT on ALL TABLES in SCHEMA pglogical to "postgres";
     GRANT SELECT on ALL SEQUENCES in SCHEMA pglogical to "postgres";
     GRANT USAGE on SCHEMA public to "postgres";
     GRANT SELECT on ALL TABLES in SCHEMA public to "postgres";
     GRANT SELECT on ALL SEQUENCES in SCHEMA public to "postgres";
     ALTER USER "postgres" with REPLICATION;
     ```
4. Set up Database Migration
   - Create migration job
   - Create connection profile for the source instance
   - Create connection profile for the target instance
   - Set the correct allowlist for the source instance (primary and outgoing IP)
   - Start migration job
   - Wait for initial load replication...

## Phase 2: Promotion

When the replica is up-to-date

1. Scale the app down to 0 replicas
2. When replica lag is 0, start promoting replica
3. Change the Application in the cluster to match the new instance, still with 0 replicas
   - Remember not to delete old instances
4. Wait for promotion complete
5. Fix ownership in the database
6. Scale up the app to the desired number of replicas
7. Take an explicit backup after upgrading

## Phase 3: Cleanup

Once the migration is verified and everything is working as it should

1. Delete migration job
2. Delete the connection profile
3. Delete "master" instance
4. Delete old resources
   - SQLInstance
   - SQLUSers
   - SQLSSLCert
