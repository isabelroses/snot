## Snot

Snot is a tangled knot that shims public repos that you own on forgejo.

This is very much a hack. So feel free to use or not to use this. At your own respective risk.

### Setup

#### Config

At this point basically every option is required to be set. All options also
exist as env vars with the prefix `SNOT`. This may look like `SNOT_WEBHOOK_SECRET`.

```toml
hostname    = "knot.example.com"
listen_addr = "0.0.0.0:5555"
owner_did   = "did:plc:abc123"
db_dsn      = "postgres:///forgejo"
repo_root   = "/var/lib/forgejo/repositories"
push_remote = "git@git.example.com"
state_dir   = "/var/lib/snot"
plc_url     = "https://plc.directory"
webhook_secret = "..."

[users]
"did:plc:abc123" = "isabel"
```

In order to get tangled to receive your push events, you will need to setup a
forgejo system level webhook.

In Forgejo: Site Administration -> Integrations -> Webhooks -> add a **system webhook**
   - Target URL: `https://<knot-host>/hooks/forgejo`
   - HTTP method: `POST`
   - Post content type: `application/json`
   - Secret: the same `webhook_secret`, likely generated via `openssl rand --hex 32`
   - Trigger: Push events

If using postgres like me (which is the only tested option right now) you will
need to setup a `snot` user and give it access to the forgejo data. For me that
looked like the following:

```sql
GRANT CONNECT ON DATABASE forgejo TO "snot";
ALTER DEFAULT PRIVILEGES FOR ROLE forgejo GRANT SELECT ON TABLES TO "snot";
GRANT SELECT ON ALL TABLES IN SCHEMA public TO "snot";
```

#### Registering the Knot

Its same as the tangled knot.

#### Creating a repo.

1. Create a repo on the forgejo frontend however you like and give it a name.
2. On the tangled frontend create a repo on your snot, with the same name as
   the repo you just made.
3. Thats all!

#### Pushing

You push to whatever you have your forgejo setup to push to, I assume there on
the same server anyways.
