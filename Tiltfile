# Tiltfile for AIM Engine controller dev

load('ext://restart_process', 'docker_build_with_restart')  # restart_process extension

# Safety: only run against dev/test clusters
allow_k8s_contexts([
    'vcluster_alex-aim-dev_alex-aim-dev_kaiwo-tw',
    'vcluster_alex-test_alex-test_app-dev',
])

update_settings(max_parallel_updates=3, k8s_upsert_timeout_secs=60)

# ---------------------------------------------------------------------------
# Image build: only rebuild when go.mod / go.sum change
# ---------------------------------------------------------------------------

IMAGE = os.getenv('DEV_IMG', 'ghcr.io/silogen/aim-engine-dev:0.0.1')

docker_build_with_restart(
    IMAGE,
    context='.',                 # full context so files are eligible for sync
    dockerfile='Dockerfile',     # dev stage inside this Dockerfile (target=dev if you use one)
    target='dev',                # drop this line if you don't use a dev stage
    entrypoint='/workspace/manager',  # command to (re)run after live_update
    ignore=['config/**/*'],      # Don't rebuild image when K8s config changes

    # Live Update: everything except dependency changes
    live_update=[
        # If deps Dockerfile change, fall back to a full image build + deploy
        fall_back_on(['go.mod', 'go.sum', 'Dockerfile']),

        # Sync Go sources into the running container
        sync('./cmd',      '/workspace/cmd'),
        sync('./internal', '/workspace/internal'),
        sync('./api',      '/workspace/api'),

        # Optional: keep tests fast in-container if you want
        # run('cd /workspace && go test ./...', trigger=['./cmd', './internal', './api']),

        # Rebuild manager binary in-place when Go code changes
        run(
            'cd /workspace && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o manager ./cmd/main.go',
            trigger=['./cmd', './internal', './api'],
        ),
    ],
)

# ---------------------------------------------------------------------------
# K8s resources
# ---------------------------------------------------------------------------

yaml = kustomize('config/tilt')

# Replace the hardcoded image with our dev image
# The kustomization.yaml has: ghcr.io/amd-enterprise-ai/aim-engine:v-e2e
yaml = blob(str(yaml).replace(
    'ghcr.io/amd-enterprise-ai/aim-engine:v-e2e',
    IMAGE
))

k8s_yaml(yaml)

k8s_resource(
    workload='aim-engine-controller-manager',
    new_name='aim-controller',
    port_forwards=[
        '8080:8080',  # metrics
        '9443:9443',  # webhook
    ],
    extra_pod_selectors=[{'control-plane': 'controller-manager'}],
)

# ---------------------------------------------------------------------------
# Optional helpers (you can re-add tests/lint/buttons later if you want)
# ---------------------------------------------------------------------------

print("""
AIM Engine – Tilt dev
  tilt up   : start dev loop
  tilt down : stop + clean
  UI        : http://localhost:10350
""")
