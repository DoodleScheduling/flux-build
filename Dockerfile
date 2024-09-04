FROM gcr.io/distroless/static:latest@sha256:95eb83a44a62c1c27e5f0b38d26085c486d71ece83dd64540b7209536bb13f6d
WORKDIR /
COPY flux-build flux-build

ENTRYPOINT ["/flux-build"]
