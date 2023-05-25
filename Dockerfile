FROM gcr.io/distroless/static:latest
WORKDIR /
COPY flux-build flux-build

ENTRYPOINT ["/flux-build"]
