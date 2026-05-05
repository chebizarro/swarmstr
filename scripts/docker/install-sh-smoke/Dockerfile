FROM debian:bookworm-slim

ARG METIQ_TASK_VERSION=v3.50.0

RUN set -eux; \
  for attempt in 1 2 3; do \
    if apt-get update -o Acquire::Retries=3; then break; fi; \
    echo "apt-get update failed (attempt ${attempt})" >&2; \
    if [ "${attempt}" -eq 3 ]; then exit 1; fi; \
    sleep 3; \
  done; \
  apt-get -o Acquire::Retries=3 install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    shellcheck \
  && sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin ${METIQ_TASK_VERSION} \
  && task --version \
  && rm -rf /var/lib/apt/lists/*

COPY install-sh-common/verify.sh /usr/local/install-sh-common/verify.sh
COPY install-sh-smoke/run.sh     /usr/local/bin/metiq-install-smoke
RUN chmod +x /usr/local/bin/metiq-install-smoke

ENTRYPOINT ["/usr/local/bin/metiq-install-smoke"]
