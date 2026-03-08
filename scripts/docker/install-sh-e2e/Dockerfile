FROM debian:bookworm-slim

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    shellcheck \
  && rm -rf /var/lib/apt/lists/*

# Common verification helper shared across smoke environments.
COPY install-sh-common/verify.sh /usr/local/lib/swarmstr/verify.sh

COPY install-sh-e2e/run.sh /run.sh
RUN chmod +x /run.sh

CMD ["/run.sh"]
