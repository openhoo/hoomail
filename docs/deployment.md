# Deployment and release operations

Hoomail is a single-container Go application with three TCP listeners and one writable SQLite database:

| Protocol | Container default | Purpose |
| --- | ---: | --- |
| HTTP | `3000` | UI, API, and server-sent events |
| SMTP | `2525` | Message ingestion |
| POP3 | `3110` | Mailbox retrieval |

The production image is `scratch`-based, runs as UID/GID `65532:65532`, has no shell, and writes its database to `/app/data/hoomail.db`. Kubernetes deploys exactly one replica because the database is a single writable SQLite file.

## Container deployment

### Published images and safe pinning

Release images are mirrored to:

- GitHub Container Registry: `ghcr.io/openhoo/hoomail`
- Docker Hub: `openhoo/hoomail`

Each release publishes these tag forms to both registries:

| Tag | Example | Stability |
| --- | --- | --- |
| Exact semantic version | `0.4.0` | Best tag choice when a digest is unavailable, but registry immutability is not enforced |
| Major/minor | `0.4` | Moving tag; advances to later patch releases |
| Release commit | `sha-1a2b3c4` | Identifies the seven-character commit of the generated release tag |
| Latest | `latest` | Moving tag |

For reproducible deployment, pin the multi-platform index digest:

```sh
docker pull ghcr.io/openhoo/hoomail@sha256:<64-lowercase-hex-characters>
```

The workflow pushes the same image index digest to both registries, but tags are not an immutability boundary. Record the resolved digest in deployment configuration rather than assuming even an exact-version tag cannot be changed.

### Prepare writable storage

A fresh Docker named volume is commonly owned by root. Hoomail cannot repair it because the runtime image has no shell and the process is deliberately non-root. Initialize the volume once before starting Hoomail:

```sh
docker volume create hoomail-data

docker run --rm \
  --user 0:0 \
  --volume hoomail-data:/data \
  alpine:3.22 \
  chown 65532:65532 /data
```

Then run the pinned image:

```sh
docker run --detach \
  --name hoomail \
  --restart unless-stopped \
  --publish 127.0.0.1:3000:3000 \
  --publish 127.0.0.1:2525:2525 \
  --publish 127.0.0.1:3110:3110 \
  --volume hoomail-data:/app/data \
  ghcr.io/openhoo/hoomail@sha256:<digest>
```

For a bind mount, create the directory and assign the same ownership first:

```sh
sudo install -d -o 65532 -g 65532 /srv/hoomail-data
```

Only `/app/data` needs to be writable. Do not assume that the image's `VOLUME` declaration makes a fresh named volume writable by UID/GID `65532`.

### Listener and host-port remapping

The Dockerfile's `EXPOSE 3000 2525 3110` entries are metadata; they do not remap listeners. If an environment variable changes a listener, the container side of `--publish HOST:CONTAINER` must use that new port.

For example, this keeps the usual host ports while moving all three listeners inside the container:

```sh
docker run --detach \
  --name hoomail \
  --env PORT=3001 \
  --env HOOMAIL_SMTP_PORT=2526 \
  --env HOOMAIL_POP3_PORT=3111 \
  --publish 127.0.0.1:3000:3001 \
  --publish 127.0.0.1:2525:2526 \
  --publish 127.0.0.1:3110:3111 \
  --volume hoomail-data:/app/data \
  ghcr.io/openhoo/hoomail@sha256:<digest>
```

Container defaults:

| Variable | Default | Contract |
| --- | --- | --- |
| `PORT` | `3000` | HTTP listener and HTTP portion of the built-in healthcheck |
| `HOOMAIL_SMTP_PORT` | `2525` | SMTP listener and SMTP portion of the built-in healthcheck |
| `HOOMAIL_POP3_PORT` | `3110` | POP3 listener and POP3 portion of the built-in healthcheck |
| `HOOMAIL_DB_PATH` | `/app/data/hoomail.db` | SQLite database path; its parent directory must be writable |
| `HOOMAIL_HEALTHCHECK_HOST` | `127.0.0.1` | Optional healthcheck target override; normally leave unchanged in a container |

### Exact healthcheck depth

The image healthcheck runs every 30 seconds, with a 3-second timeout, 3-second start period, and three retries. It executes `/hoomail healthcheck`, which performs exactly these checks:

1. `GET /api/mailboxes` and require HTTP `200 OK`.
2. Establish and close an SMTP TCP connection. It does **not** send a message or validate an SMTP greeting.
3. Establish a POP3 TCP connection, read one line, and require a greeting beginning with `+OK`.

A healthy status therefore proves that all three listeners meet those checks. It does not prove message delivery, database persistence, external routing, TLS, or full SMTP/POP3 command behavior.

Inspect it with:

```sh
docker inspect --format '{{json .State.Health}}' hoomail
docker logs hoomail
```

## Helm deployment

The chart is available under `charts/hoomail` in the source tree, and a matching packaged chart archive is attached to each GitHub Release.

Install from a source checkout:

```sh
helm upgrade --install hoomail ./charts/hoomail \
  --namespace hoomail \
  --create-namespace \
  --wait
```

Inspect the resulting objects and endpoints:

```sh
kubectl get deployment,pod,service,pvc,ingress --namespace hoomail
helm get values hoomail --namespace hoomail
helm get manifest hoomail --namespace hoomail
```

### Workload invariants and lifecycle

| Key | Default | Behavior |
| --- | --- | --- |
| `replicaCount` | `1` | Schema-enforced constant. The chart does not support multiple replicas against the writable SQLite database. |
| `revisionHistoryLimit` | `1` | Number of retained Deployment revisions; `0` is allowed. |
| Deployment strategy | `Recreate` | Fixed by the template. The old Pod stops before the replacement starts, preventing overlapping writers but causing upgrade downtime. |
| Database path | `/app/data/hoomail.db` | Fixed by the chart rather than exposed as a Helm value. |

Plan for a brief outage on every Pod-replacing upgrade. `Recreate` and one replica are data-safety choices, not settings to work around with an unsupported scale-up.

### Image values

| Key | Default | Notes |
| --- | --- | --- |
| `image.repository` | `ghcr.io/openhoo/hoomail` | Set `openhoo/hoomail` to use Docker Hub. |
| `image.pullPolicy` | `IfNotPresent` | Allowed values are `Always`, `IfNotPresent`, and `Never`. |
| `image.tag` | `""` | Empty means the chart's `appVersion`. |
| `image.digest` | `""` | When set, the rendered image is `repository@digest` and this value takes precedence over `image.tag`. Must be `sha256:` followed by exactly 64 lowercase hexadecimal characters. |
| `imagePullSecrets` | `[]` | Standard Pod image-pull secret references, for example `[{name: registry-credentials}]`. Also used by the Helm test Pod. |

A digest-pinned installation:

```sh
helm upgrade --install hoomail ./charts/hoomail \
  --namespace hoomail \
  --create-namespace \
  --set-string image.digest='sha256:<64-lowercase-hex-characters>' \
  --wait
```

If both `image.tag` and `image.digest` are supplied, the digest wins. The unused tag is not included in the rendered image reference.

### Resources

The default resource object is deliberately small:

| Resource | Request | Limit |
| --- | ---: | ---: |
| CPU | `5m` | none |
| Memory | `16Mi` | `64Mi` |

Large inboxes, large MIME messages, attachments, or unusually heavy concurrent use can require a higher memory limit. Override the standard Kubernetes `resources` object, for example:

```yaml
resources:
  requests:
    cpu: 20m
    memory: 64Mi
  limits:
    memory: 256Mi
```

### Security, service accounts, and RBAC

Default Pod security context:

```yaml
podSecurityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  fsGroupChangePolicy: OnRootMismatch
  seccompProfile:
    type: RuntimeDefault
```

Default container security context:

```yaml
securityContext:
  allowPrivilegeEscalation: false
  capabilities:
    drop: [ALL]
  privileged: false
  readOnlyRootFilesystem: true
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
```

The root filesystem remains read-only; the data volume mounted at `/app/data` is the writable area.

Service-account behavior:

| Key | Default | Behavior |
| --- | --- | --- |
| `serviceAccount.create` | `false` | No ServiceAccount is created. |
| `serviceAccount.name` | `""` | With `create: false`, empty selects the namespace `default` account; a value selects that existing account. With `create: true`, empty uses the chart fullname. |
| `serviceAccount.annotations` | `{}` | Applied only when the chart creates the ServiceAccount. |
| `serviceAccount.automount` | `false` | Schema-enforced constant. The Pod and a chart-created account both disable token automounting. |

The chart creates no `Role`, `ClusterRole`, `RoleBinding`, or `ClusterRoleBinding`. Hoomail does not receive Kubernetes API credentials. Selecting or creating a ServiceAccount does not grant it permissions, and token automount cannot be enabled through chart values.

### Probe behavior and defaults

Startup, readiness, and liveness probes are exec probes that run `/hoomail healthcheck` inside the application container. They therefore use the exact HTTP-200, SMTP-connect, and POP3-`+OK` checks described in [Exact healthcheck depth](#exact-healthcheck-depth).

| Probe | Enabled | Initial delay | Period | Timeout | Failure threshold |
| --- | --- | ---: | ---: | ---: | ---: |
| `startupProbe` | `true` | `0s` | `2s` | `3s` | `30` |
| `readinessProbe` | `true` | `1s` | `10s` | `3s` | `3` |
| `livenessProbe` | `true` | `5s` | `30s` | `3s` | `3` |

Each probe supports only:

- `enabled`
- `initialDelaySeconds`
- `periodSeconds`
- `timeoutSeconds`
- `failureThreshold`

The chart does not expose a custom probe command, HTTP path, or success threshold. Disabling one probe does not change the other two.

## Kubernetes networking

### Understand the port layers

The chart has two independent Kubernetes port layers:

| Layer | Values | Default | Meaning |
| --- | --- | --- | --- |
| Application/container listeners | `ports.http`, `ports.smtp`, `ports.pop3` | `3000`, `2525`, `3110` | Passed to the Hoomail process and declared as named container ports. |
| Shared Service ports | `service.ports.http`, `service.ports.smtp`, `service.ports.pop3` | `3000`, `2525`, `3110` | Client-facing ports on the one Kubernetes Service. Each targets the corresponding named container port. |

These values do not have to be numerically equal. For example, the Service can expose SMTP on port `25` while Hoomail continues listening on container port `2525`:

```yaml
ports:
  smtp: 2525
service:
  ports:
    smtp: 25
```

Changing `ports.*` changes the process listener. Changing `service.ports.*` changes only the Service's client-facing port. The Helm test reads `service.ports.*` and reaches the named Service, while the application Pod probes use `ports.*` through the container environment.

### One shared three-port Service

The chart creates one Service containing HTTP, SMTP, and POP3 together:

| Key | Default | Notes |
| --- | --- | --- |
| `service.type` | `ClusterIP` | Allowed: `ClusterIP`, `NodePort`, or `LoadBalancer`. The selected type applies to all three ports. |
| `service.annotations` | `{}` | Shared Service annotations; useful for provider-specific LoadBalancer integration. |
| `service.ports.http` | `3000` | HTTP Service port. |
| `service.ports.smtp` | `2525` | SMTP Service port. |
| `service.ports.pop3` | `3110` | POP3 Service port. |

Important limitations:

- There are no independent HTTP, SMTP, and POP3 Services.
- `NodePort` exposes all three ports and lets Kubernetes allocate node ports. The chart has no values for fixed `nodePort` numbers.
- `LoadBalancer` exposes all three unauthenticated protocols together. Provider behavior can be influenced through shared `service.annotations`, but the chart does not supply authentication or expose dedicated values for `loadBalancerIP`, load-balancer class, source ranges, or external traffic policy.
- The chart cannot keep HTTP as `ClusterIP` while exposing only SMTP/POP3 as `LoadBalancer` or `NodePort`. That topology requires additional Services maintained outside this chart or a chart change.

Warning: Hoomail does not authenticate HTTP, SMTP, or POP3 clients. A `LoadBalancer` publishes all three through the same Service. Use it only with an internal/private load balancer or provider/firewall rules that restrict every port to trusted networks. Configure those controls outside this chart; the example below does not create them.

Expose the shared Service through a cloud load balancer:

```sh
helm upgrade --install hoomail ./charts/hoomail \
  --namespace hoomail \
  --create-namespace \
  --set service.type=LoadBalancer \
  --wait

kubectl get service hoomail --namespace hoomail --watch
```

`NodePort` likewise exposes all three unauthenticated protocols on cluster nodes. Use it only where node firewalls and network boundaries restrict every allocated port to trusted clients; the chart does not configure those controls.

For `NodePort`, discover the automatically allocated ports after installation:

```sh
helm upgrade --install hoomail ./charts/hoomail \
  --namespace hoomail \
  --create-namespace \
  --set service.type=NodePort \
  --wait

kubectl get service hoomail --namespace hoomail
```

### Local access with ClusterIP

The default Service is cluster-internal. Forward all three protocols for local development:

```sh
kubectl port-forward --namespace hoomail service/hoomail \
  3000:3000 \
  2525:2525 \
  3110:3110
```

This command remains attached to the terminal and is not a production exposure mechanism. Adjust the right-hand ports if `service.ports.*` was changed, and adjust the Service name if the release or fullname was changed.

### HTTP-only Ingress

The optional chart-managed Kubernetes Ingress routes only the Service's named `http` port. It does not expose SMTP or POP3 and does not create controller-specific TCP stream mappings.

To publish SMTP or POP3 through an ingress controller, configure that controller's separate TCP-stream feature outside this chart. Ordinary Kubernetes `Ingress` rules are HTTP(S) only.

Ingress values:

| Key | Default | Notes |
| --- | --- | --- |
| `ingress.enabled` | `false` | Creates no Ingress unless enabled. |
| `ingress.className` | `""` | Optional `spec.ingressClassName`. |
| `ingress.annotations` | `{}` | Controller/certificate annotations. |
| `ingress.hosts` | `hoomail.localhost` with `/` and `Prefix` | One or more hosts, each with one or more `{path, pathType}` entries. Allowed path types: `Exact`, `Prefix`, `ImplementationSpecific`. |
| `ingress.tls` | `[]` | Standard Kubernetes Ingress TLS entries. |

Hoomail's HTTP interface has no built-in authentication. Enable Ingress only behind an authentication-capable ingress controller or reverse proxy, and restrict access to trusted networks. Configure authentication and network controls outside this chart. TLS encrypts traffic but does not authenticate users or make public exposure safe.

Minimal HTTP ingress values:

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: mail.example.test
      paths:
        - path: /
          pathType: Prefix
  tls: []
```

Install them with:

```sh
helm upgrade --install hoomail ./charts/hoomail \
  --namespace hoomail \
  --create-namespace \
  --values ingress-values.yaml \
  --wait

kubectl get ingress --namespace hoomail
```

The same requirements apply when enabling TLS: the ingress controller or reverse proxy must enforce authentication and trusted-network access. The chart supplies neither control, and TLS alone is insufficient.

TLS termination example:

```yaml
ingress:
  enabled: true
  className: nginx
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-production
  hosts:
    - host: mail.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: hoomail-http-tls
      hosts:
        - mail.example.com
```

An authentication-capable ingress controller or reverse proxy, trusted-network controls, working DNS, and the referenced TLS Secret or certificate automation must already exist. TLS terminates at the ingress controller; Hoomail remains cleartext HTTP behind it. TLS alone does not authenticate clients or make public exposure safe, and the chart does not supply authentication or source-range restrictions. Helm's install notes print only a host-level hint, so use the configured path and verify that each HTTPS host appears in a matching TLS entry rather than treating that hint as authoritative.

## Persistence

Hoomail uses a Deployment, not a StatefulSet. Its one data volume is mounted at `/app/data`.

### Generated PVC: default

Defaults:

```yaml
persistence:
  enabled: true
  existingClaim: ""
  storageClass: ""
  accessModes:
    - ReadWriteOnce
  size: 256Mi
  annotations: {}
  selector: {}
```

With these values the chart creates a Helm-managed PVC named from the release fullname. An empty `storageClass` omits `storageClassName`, so provisioning relies on the cluster's default StorageClass. If the cluster has no usable default provisioner, the claim and Pod can remain pending.

For another dynamic StorageClass:

```yaml
persistence:
  enabled: true
  storageClass: fast-block
  accessModes: [ReadWriteOnce]
  size: 1Gi
```

For static provisioning, set `persistence.selector` to labels that select the intended PV. Ensure the access mode, requested size, storage class, and PV labels are compatible.

### Existing PVC

Reuse a claim in the same namespace:

```yaml
persistence:
  enabled: true
  existingClaim: hoomail-data
```

The chart does not create, resize, annotate, or otherwise manage an existing claim. `storageClass`, `accessModes`, `size`, `annotations`, and `selector` apply only to a newly generated PVC and are ineffective when `existingClaim` is set.

The mounted filesystem must be writable by UID/GID `65532`. The default Pod context requests `fsGroup: 65532` with `fsGroupChangePolicy: OnRootMismatch`, but storage-driver support determines whether Kubernetes can apply that ownership. CSI drivers or pre-provisioned storage that do not honor `fsGroup` require ownership/permissions to be prepared on the storage side.

### Ephemeral `emptyDir`

For disposable installations only:

```yaml
persistence:
  enabled: false
```

The chart then mounts `emptyDir`. All mail, attachments, calendar state, read state, and other SQLite data are lost when the Pod is replaced, including a normal `Recreate` upgrade. `existingClaim` is ignored while persistence is disabled.

### Uninstall and retention

A generated PVC has no keep policy by default. Because it is a Helm-owned resource, `helm uninstall` can delete it. What happens to the backing PV afterward depends on the StorageClass/PV reclaim policy; do not assume the data is retained.

For intentional retention, prefer a separately managed existing claim, or explicitly annotate the generated claim:

```yaml
persistence:
  annotations:
    helm.sh/resource-policy: keep
```

Record and test the corresponding PV reclaim policy as a separate storage concern. Before uninstalling, inspect the actual claim and PV:

```sh
kubectl get pvc --namespace hoomail
kubectl get pv
helm uninstall hoomail --namespace hoomail
```

A kept claim becomes an independently retained resource and may need to be selected later through `persistence.existingClaim`.

## Helm test

Run the chart's test after installation or upgrade:

```sh
helm test hoomail --namespace hoomail --logs
```

The hook Pod:

- uses the same image repository/tag/digest and pull policy as the workload;
- uses the configured `imagePullSecrets`;
- runs as non-root UID/GID `65532` with a read-only root filesystem, dropped capabilities, `RuntimeDefault` seccomp, and no service-account token;
- targets the release's shared Service through cluster DNS and its configured `service.ports.*` values;
- executes the built-in Hoomail healthcheck.

Its scope is intentionally limited to in-cluster HTTP `200`, SMTP TCP acceptance, and POP3 `+OK`. It does **not**:

- deliver or retrieve a message;
- verify SQLite or PVC persistence;
- test an Ingress, TLS, NodePort, LoadBalancer, or any external route;
- validate SMTP greeting/content or full POP3 behavior.

The hook has no delete policy or TTL, so a completed test Pod can remain. Inspect and remove completed hook Pods when needed:

```sh
kubectl get pods --namespace hoomail \
  --selector app.kubernetes.io/instance=hoomail

kubectl delete pod --namespace hoomail \
  --selector app.kubernetes.io/instance=hoomail \
  --field-selector status.phase==Succeeded
```

A private registry deployment must permit both the workload Pod and this test Pod to pull the selected image.

## Metadata, naming, and scheduling values

| Key | Default | Behavior |
| --- | --- | --- |
| `nameOverride` | `""` | Overrides the chart name portion used in generated resource names and labels. |
| `fullnameOverride` | `""` | Replaces the generated release fullname, truncated to Kubernetes naming limits. |
| `podAnnotations` | `{}` | Added to the workload Pod template. |
| `podLabels` | `{}` | Added to the workload Pod template. Must not define `app.kubernetes.io/name` or `app.kubernetes.io/instance`; rendering fails if either reserved selector label is supplied. |
| `nodeSelector` | `{}` | Standard Pod node selector. |
| `tolerations` | `[]` | Standard Pod tolerations. |
| `affinity` | `{}` | Standard Pod affinity/anti-affinity. |
| `topologySpreadConstraints` | `[]` | Standard Pod topology spread constraints. With one replica these affect placement, not high availability. |

The chart's selector labels are stable ownership selectors. Put organization-specific labels in `podLabels` without overriding the two reserved keys.

## Release operations

### Trigger and version flow

Automatic release starts only after a successful CI workflow that:

- was triggered by a `push`;
- ran for `main`;
- came from this repository rather than a fork;
- corresponds to the current `main` commit rather than a stale workflow run;
- is not the generated `chore(release):` commit.

Maintainers can also dispatch the `Release` workflow manually. A manual dry run previews Hooversion without release commits, tags, GitHub Releases, chart publication, or images:

```sh
gh workflow run Release \
  --repo openhoo/hoomail \
  --ref main \
  --field dry_run=true
```

Hooversion is pinned to commit `799ecf4a9c29e8ce4d5ad7055a6030adf665cf82` (`v0.2.0`). Conventional Commits drive the version. The configured release scopes are `hoomail`, `client`, `server`, `smtp`, `docker`, `ghcr`, `image`, `helm`, `chart`, and `release`.

When a release is published, automation:

1. updates `internal/version/version` and `CHANGELOG.md`;
2. runs `bun scripts/sync-chart-version.ts` so chart and application versions match;
3. creates the release commit, tag, and GitHub Release;
4. packages `charts/hoomail` and uploads its `.tgz` archive to that GitHub Release;
5. builds and pushes the multi-platform image to GHCR and Docker Hub.

Release automation can advance `main` with the generated release commit. Maintainers should update their local branch after the workflow completes.

### Required credentials

| Credential | Use |
| --- | --- |
| Repository `GITHUB_TOKEN` | Hooversion/GitHub Release updates, GHCR authentication, package publication, and GitHub artifact attestation |
| `DOCKERHUB_USERNAME` | Docker Hub login |
| `DOCKERHUB_TOKEN` | Docker Hub login token |

`DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` are organization/repository Actions secrets. They must not be placed in chart values or application configuration.

### Image publication properties

Release images target:

- `linux/amd64`
- `linux/arm64`

The image build uses zstd-compressed OCI layers at compression level 19, embeds the release version in the binary, and publishes:

- BuildKit SBOM attestations (`sbom: true`);
- maximum-mode BuildKit provenance (`provenance: mode=max`);
- OCI labels for revision, version, source, and `org.opencontainers.image.licenses=Apache-2.0`.

The same built index digest is pushed to GHCR and Docker Hub. In addition, GitHub Actions publishes a GitHub artifact attestation whose subject name is the **GHCR** image and whose subject is that index digest. The workflow does not publish a separate GitHub artifact attestation for the Docker Hub subject and contains no separate Docker Hub signing step. Do not describe or operationally assume the Docker Hub reference is independently GitHub-attested or signed.

For supply-chain policy, distinguish these artifacts rather than treating “SBOM,” “provenance,” “artifact attestation,” and “signature” as interchangeable terms.

## Source references

The operational contracts in this document are defined by:

- [Dockerfile](../Dockerfile) — scratch runtime, UID/GID, default ports/database path, and Docker healthcheck schedule.
- [`cmd/hoomail/main.go`](../cmd/hoomail/main.go) — exact listener environment and healthcheck behavior.
- [`internal/store/store.go`](../internal/store/store.go) — database directory creation and writable-path requirement.
- [`charts/hoomail/values.yaml`](../charts/hoomail/values.yaml) and [`values.schema.json`](../charts/hoomail/values.schema.json) — supported Helm keys, defaults, and schema invariants.
- [`charts/hoomail/templates/deployment.yaml`](../charts/hoomail/templates/deployment.yaml) — Recreate strategy, fixed database path, probes, security, storage, and scheduling.
- [`service.yaml`](../charts/hoomail/templates/service.yaml) and [`ingress.yaml`](../charts/hoomail/templates/ingress.yaml) — shared three-port Service and HTTP-only Ingress.
- [`pvc.yaml`](../charts/hoomail/templates/pvc.yaml) — generated-PVC behavior.
- [`serviceaccount.yaml`](../charts/hoomail/templates/serviceaccount.yaml) — optional account creation and disabled token automount.
- [`test-connection.yaml`](../charts/hoomail/templates/tests/test-connection.yaml) — Helm test execution and security context.
- [Release workflow](../.github/workflows/release.yml) and [Hooversion configuration](../hooversion.config.ts) — release triggers, credentials, tags, platforms, chart packaging, SBOM/provenance, attestation, and OCI metadata.
