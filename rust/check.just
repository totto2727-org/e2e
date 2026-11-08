set shell := ["bash", "-euo", "pipefail", "-c"]

[default]
all: rustfmt clippy

rustfmt:
    cargo fmt --all --check

clippy:
    cargo clippy --locked --all-targets --all-features -- -D warnings
