# Deploy Pipkin

## Docker Compose

Docker Compose 2.30 or later is required. Run these commands from the Pipkin checkout. The default workspace is this checkout. To work on another project, set `PIPKIN_WORKSPACE` to its absolute path in your shell or in the checkout’s `.env`.

```sh
docker compose up -d pipkin
```

Open <http://127.0.0.1:8787>. For a local build from source, use `docker compose up -d --build pipkin`. Until the first GitHub image release is published, use the local build command.

The selected workspace is mounted read/write for approved edits. Deliveries persist under the mounted workspace’s `.pipkin/outputs`. Set `PIPKIN_UID` and `PIPKIN_GID` to your host IDs if they differ from 1000. On Linux, use `id -u` and `id -g` to find them.

Put model keys and environment overrides in the checkout’s `.env`, or set `PIPKIN_DOCKER_ENV` to another environment file. To use named profiles, place `config.json` in `deploy/` or set `PIPKIN_CONFIG_DIR` to the directory containing it. For hosted providers, use the keys and profile settings from the main README. Change `PIPKIN_PORT` if port 8787 is already in use.

Compose publishes Pipkin on host loopback. Keep the web port bound to loopback and use a trusted Docker network because the web UI has no login layer. The container’s listener must be reachable from Docker’s port forwarding, which is the purpose of `PIPKIN_WEB_CONTAINER=1`. Native Pipkin defaults to loopback.

## Optional local inference with vLLM

The vLLM profile uses an NVIDIA GPU and requires the NVIDIA Container Toolkit. Its model cache persists in a Docker volume. Add these settings to the Compose `.env` when using the default example model.

```dotenv
VLLM_MODEL=Qwen/Qwen3-0.6B
PIPKIN_PROVIDER=vllm
PIPKIN_BASE_URL=http://vllm:8000/v1
PIPKIN_MODEL=Qwen/Qwen3-0.6B
```

If you select a named Pipkin provider profile, configure the endpoint and model there. Keep the model ID the same as `VLLM_MODEL`. You can override `VLLM_IMAGE`, `VLLM_MAX_MODEL_LEN`, and `HF_TOKEN` if needed.

```sh
docker compose --profile vllm up -d
docker compose logs -f vllm
```

Wait for the inference server to finish loading the model before sending a request. vLLM’s HTTP port is reachable from Pipkin over the Compose network. Using an existing vLLM instance instead only requires an endpoint reachable from the Pipkin container.

## Optional LangGraph

The published container includes Python and LangGraph. Source builds include them by default under Compose. Set `PIPKIN_WITH_LANGGRAPH=false` for a smaller local build if you use only the built-in runner.

Merge the fields in [config.langgraph.example.json](config.langgraph.example.json) into the mounted Pipkin config. The container supplies Python on `PATH`. If your config has a host-specific `langgraph_python`, remove it for this deployment or replace it with `/opt/langgraph/bin/python`. Enable agentic mode for the delivery to use the profile.

## Optional Langfuse

Use an existing HTTPS Langfuse instance by setting `LANGFUSE_BASE_URL` and the project keys in the Compose `.env`. To self-host locally, fetch the pinned upstream Compose file and combine it with Pipkin’s loopback overlay.

```sh
curl -fsSLo deploy/langfuse.upstream.yaml https://raw.githubusercontent.com/langfuse/langfuse/v4.85.0/docker-compose.yml
```

Set `LANGFUSE_IMAGE_TAG=4.85.0` and configure the upstream file’s credential placeholders before starting it. Langfuse needs its own database, ClickHouse, Redis, and object storage credentials in addition to its app secrets. For their variable names and generation guidance, see the [upstream self-hosting documentation](https://langfuse.com/self-hosting/deployment/docker-compose).

```sh
docker compose -f compose.yaml -f deploy/langfuse.upstream.yaml -f compose.langfuse.yaml \
  up -d pipkin langfuse-web langfuse-worker
```

Open <http://127.0.0.1:3000>, create a project and keys, then set `LANGFUSE_PUBLIC_KEY` and `LANGFUSE_SECRET_KEY` in Pipkin’s environment file. Run the same Compose command again to recreate Pipkin with the keys. For this overlay, omit `langfuse_url` in Pipkin’s JSON config so the container can use the overlay’s internal service URL. To include vLLM in the same run, add `--profile vllm` before `up` and add `vllm` to the service list.

## Release binaries and installer

The release workflow builds Linux and macOS archives for amd64 and arm64, emits SHA-256 checksums, and publishes a Linux multi-architecture container to GHCR on a version tag. Ensure the GHCR package is public before advertising anonymous pulls.

Download an archive from [GitHub Releases](https://github.com/JakeKalstad/pipkin/releases), or install the latest release into `~/.local/bin`:

```sh
curl -fsSL https://github.com/JakeKalstad/pipkin/releases/latest/download/install.sh | sh
```

You can also download and inspect the script first, then run `sh install.sh`. Set `PIPKIN_VERSION=v0.15.0` for a specific published tag or `PIPKIN_INSTALL_DIR=/your/bin` to change the destination. When piping, put those variables on the `sh` side, for example `curl ... | PIPKIN_VERSION=v0.15.0 sh`. The installer verifies the release asset’s checksum before installing and uses no sudo. The binary installer does not install the editor extension.
