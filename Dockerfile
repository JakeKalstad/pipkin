# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false -ldflags="-s -w" -o /out/pipkin ./cmd/pipkin

FROM debian:bookworm-slim
ARG WITH_LANGGRAPH=false
COPY requirements-langgraph.txt /tmp/requirements-langgraph.txt
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates git; \
    if [ "$WITH_LANGGRAPH" = true ]; then \
      apt-get install -y --no-install-recommends python3 python3-venv; \
      python3 -m venv /opt/langgraph; \
      /opt/langgraph/bin/pip install --no-cache-dir -r /tmp/requirements-langgraph.txt; \
    fi; \
    rm -rf /var/lib/apt/lists/* /tmp/requirements-langgraph.txt
COPY --from=build /out/pipkin /usr/local/bin/pipkin
ENV HOME=/tmp \
    PATH=/opt/langgraph/bin:$PATH \
    LISTEN_URL=http://127.0.0.1:8787 \
    PIPKIN_WEB_CONTAINER=1 \
    PIPKIN_OUTPUT_DIR=/workspace/.pipkin/outputs
WORKDIR /workspace
USER 1000:1000
EXPOSE 8787
ENTRYPOINT ["pipkin"]
CMD ["--web"]
