## Build and test kustomize overlays

[![release](https://img.shields.io/github/release/DoodleScheduling/flux-kustomize-action/all.svg)](https://github.com/DoodleScheduling/flux-kustomize-action/releases)
[![release](https://github.com/doodlescheduling/flux-kustomize-action/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/flux-kustomize-action/actions/workflows/release.yaml)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/flux-kustomize-action)](https://goreportcard.com/report/github.com/DoodleScheduling/flux-kustomize-action)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/flux-kustomize-action/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/flux-kustomize-action?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/flux-kustomize-action.svg)](https://github.com/DoodleScheduling/flux-kustomize-action/blob/master/LICENSE)

Github action for testing kustomize overlays with suppport for unpacking flux HelmReleases.
This action builds a kustomization overlay similar how the behaviour of the kustomize-controller is.
The output is a yaml file containing all built resources.

While this is great the big feature is that it also includes all manifests templated from each HelmRelease.
The action templates the manifest similar how the behaviour of the helm-controller is with many features supported including referencing ConfigMaps, in-chart values and more.

Errors must be acknowledge as early as possible in a delivery pipeline. Errors emerging from HelmReleases often only occur once a HelmRelease is already applied to the cluster.
With this action manifests from a HelmRelease can be validated before appliying it to a cluster.  

### Inputs

```yaml
paths:
  description: "Comma separated paths to kustomize"
  required: true
  default: "."
workers:
  description: "Concurrent helm template workers"
  required: false
  default: "1"
fail-fast:
  description: "Abort early if any error occurs"
  required: false
  default: "false"
allow-failure:
  description: "Specify if the action should fail if any errors occurs."
  required: false
  default: "false"
cache-dir:
  description: "Path where artifacts may be stored"
  required: false
registry-credentials:
  description: ''
  required: false
```

### Outputs

```yaml
manifestFiles:
  description: "Comma separated paths to the built manifests containing all resources (per path input)"
```

### Example usage

```yaml
name: flux-kustomize-action
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: doodlescheduling/flux-kustomize-action@v1
        with:
          paths: /staging,/production
```


### Advanced example

While a simple gitops pipeline just verifies if kustomizations can be built and HelmReleases installed a more advanced pipeline
includes follow-up validations like kyverno tests, kubeval validations or kubeaudit tests.

```yaml
name: flux-kustomize-action
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: doodlescheduling/flux-kustomize-action@v1
        id: kustomize
        with:
          paths: /staging,/production
      - name: Setup kubeconform
        shell: bash
        run: |
          curl -L -v https://github.com/yannh/kubeconform/releases/download/v0.6.1/kubeconform-linux-amd64.tar.gz -o kubeconform.tgz
          tar xvzf kubeconform.tgz
          sudo mv kubeconform /usr/bin/
      - name: Convert CRD to json schemas
        shell: bash
        env:
          MANIFESTS: "${{ steps.kustomize.outputs.manifestPaths }}"
        run: |
          git clone https://github.com/yannh/kubeconform
          for m in ${MANIFESTS//,/ }; do
            mkdir "$m.schemas"
            cat $m | yq -e 'select(.kind == "CustomResourceDefinition")' > $m.schemeas/crds.yaml
            kubeconform/scripts/openapi2jsonschema.py $m.schemeas/$l.yaml
          done
          rm -rf kubeconform
      - name: Run conform
        env: 
          KUBERNETES_VERSION: "1.26"
          MANIFESTS: "${{ steps.kustomize.outputs.manifestPaths }}"
        run: |
          for m in ${MANIFESTS//,/ }; do
            kubeconform -verbose -kubernetes-version $KUBERNETES_VERSION -schema-location default -schema-location "$m.schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json" --ignore-missing-schemas --strict

            mkdir "$m.schemeas"
            cat $m | yq -e 'select(.kind == "CustomResourceDefinition")' > $m.schemeas/crds.yaml
            kubeconform/scripts/openapi2jsonschema.py $m.schemeas/$l.yaml
          done
```
