set shell := ["bash", "-euo", "pipefail", "-c"]

[default]
all: rustfmt clippy

rustfmt:
    cargo fmt --all

clippy:
    cargo clippy --fix --allow-dirty --allow-staged --locked --all-targets --all-features -- -D warnings
