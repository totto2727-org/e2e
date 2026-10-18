FROM ubuntu:24.04 AS base

WORKDIR /workspace

CMD ["sleep", "infinity"]

FROM base AS nonroot
USER nobody

FROM base AS default
