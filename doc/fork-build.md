# Build and deploy this fork from GitHub

The `Build Fork Image` workflow builds this checkout on GitHub runners. It does not use your workstation Docker daemon and does not publish a GitHub Release or overwrite `latest`.

1. Commit and push the code and `.github/workflows/build-fork-image.yml` to the default branch of `hitechr/Resin`. Do **not** commit `.env` or other credentials. Make sure GitHub Actions is enabled and its `GITHUB_TOKEN` can write packages (the job requests `packages: write`).
2. In GitHub, open **Actions > Build Fork Image > Run workflow** and select the pushed branch. Alternatively run `gh workflow run build-fork-image.yml --ref master` after it has reached the default branch.
3. Wait for the WebUI build, API tests, both Linux builds and Docker publish job to succeed. Copy the image reference from the Docker job summary: `ghcr.io/hitechr/resin:sha-<full-40-character-commit-sha>` for both `amd64` and `arm64`. Do not use the upstream `ghcr.io/resinat/resin:latest` image for these changes. A private GHCR package requires `docker login ghcr.io` on the Linux host with a token having `read:packages` permission.

Before replacing an existing Linux container, inspect its image and mounts with `docker inspect resin --format '{{.Config.Image}} {{json .Mounts}}'`. Back up its state directory or named volume and keep its environment variables, ports and mounts unchanged. The local `.env` in this repository is not automatically transferred to Linux; keep the existing Linux credentials unless you intend to rotate them separately.

Set the **existing Linux Compose service** image to the exact `ghcr.io/hitechr/resin:sha-...` reference. If using `docker-compose.yml.example`, copy its contents to your server's Compose file and set `RESIN_IMAGE` in the server's Compose environment; the example defaults to the upstream image otherwise. Then, from that Compose project directory:

```sh
docker tag "$(docker inspect resin --format '{{.Image}}')" resin:pre-upgrade
docker compose pull resin
docker compose up -d --no-build resin
docker compose ps resin
curl -f http://127.0.0.1:2260/healthz
```

Verify login and the new dashboard after the health check. If it fails, point the existing Compose service image back to `resin:pre-upgrade` and run `docker compose up -d --no-build resin`. Never use `docker compose down -v` for this upgrade: it removes named volumes. If the existing container is not Compose-managed, inspect and preserve its exact `docker run` options before recreating it.
