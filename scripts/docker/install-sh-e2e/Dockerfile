FROM debian:bookworm-slim

ARG METIQ_TASK_VERSION=v3.50.0

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    shellcheck \
  && sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin ${METIQ_TASK_VERSION} \
  && task --version \
  && rm -rf /var/lib/apt/lists/*

# Common verification helper shared across smoke environments.
COPY install-sh-common/verify.sh /usr/local/lib/metiq/verify.sh

COPY install-sh-e2e/run.sh /run.sh
RUN chmod +x /run.sh

CMD ["/run.sh"]
