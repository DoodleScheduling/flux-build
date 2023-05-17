FROM gcr.io/distroless/static:latest
WORKDIR /
COPY flux-kustomize-action flux-kustomize-action

ENTRYPOINT ["/flux-kustomize-action"]
