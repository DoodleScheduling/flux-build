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
      - uses: doodlescheduling/flux-kustomize-action@v0
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
      - uses: doodlescheduling/flux-kustomize-action@v0
        id: kustomize
        with:
          paths: /staging,/production
      - name: Setup kubeconform
        run: |
          curl -L -v --fail https://github.com/yannh/kubeconform/releases/download/v0.6.1/kubeconform-linux-amd64.tar.gz -o kubeconform.tgz
          tar xvzf kubeconform.tgz
          sudo mv kubeconform /usr/bin/
      - name: Setup openapi2jsonschema
        run: |
          curl -L -v --fail https://raw.githubusercontent.com/yannh/kubeconform/v0.6.2/scripts/openapi2jsonschema.py -o openapi2jsonschema.py
          sudo mv openapi2jsonschema.py /usr/bin/openapi2jsonschema
          sudo chmod +x /usr/bin/openapi2jsonschema
      - name: Convert CRD to json schemas
        env:
          MANIFESTS: "${{ steps.kustomize.outputs.manifestPaths }}"
        run: |
          for m in ${MANIFESTS//,/ }; do
            echo "openapi2jsonschema $m"
            mkdir "$m.schemas"
            cat $m | yq -e 'select(.kind == "CustomResourceDefinition")' > $m.schemas/crds.yaml
            openapi2jsonschema $m.schemas/*.yaml
          done
      - name: Run conform
        env: 
          KUBERNETES_VERSION: "1.26.0"
          MANIFESTS: "${{ steps.kustomize.outputs.manifestPaths }}"
        run: |
          for m in ${MANIFESTS//,/ }; do
            echo "kubeconform $m"
            cat $m | kubeconform -verbose -kubernetes-version $KUBERNETES_VERSION -schema-location default -schema-location "$m.schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json" --strict
          done
      - name: Setup kyverno
        run: |
          curl -LO --fail https://github.com/kyverno/kyverno/releases/download/v1.7.2/kyverno-cli_v1.7.2_linux_x86_64.tar.gz
          tar -xvf kyverno-cli_v1.7.2_linux_x86_64.tar.gz
          sudo cp kyverno /usr/local/bin/
      - name: Test kyverno policies
        run: |
          for m in ${MANIFESTS//,/ }; do
            echo "kyverno apply $m"
            kyverno apply base/cluster-policies -r $m
          done
```

## License notice

Many internal packages have been cloned from [source-controller](https://github.com/fluxcd/source-controller) and [helm-controller](https://github.com/fluxcd/helm-controller) to achive the same functionilty for this
action as at controller runtime.

Please see upstream [license](https://github.com/fluxcd/source-controller/blob/main/LICENSE).
