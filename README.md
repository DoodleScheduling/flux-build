## Build and test kustomize overlays with flux support

[![release](https://img.shields.io/github/release/DoodleScheduling/flux-build/all.svg)](https://github.com/DoodleScheduling/flux-build/releases)
[![release](https://github.com/doodlescheduling/flux-build/actions/workflows/release.yaml/badge.svg)](https://github.com/doodlescheduling/flux-build/actions/workflows/release.yaml)
[![report](https://goreportcard.com/badge/github.com/DoodleScheduling/flux-build)](https://goreportcard.com/report/github.com/DoodleScheduling/flux-build)
[![Coverage Status](https://coveralls.io/repos/github/DoodleScheduling/flux-build/badge.svg?branch=master)](https://coveralls.io/github/DoodleScheduling/flux-build?branch=master)
[![license](https://img.shields.io/github/license/DoodleScheduling/flux-build.svg)](https://github.com/DoodleScheduling/flux-build/blob/master/LICENSE)

Test kustomize overlays with suppport for templating flux2 HelmReleases.
Errors must be acknowledged as early as possible in a delivery pipeline. Errors emerging from HelmReleases often only occur once a HelmRelease is already applied to the cluster.
This app can be used locally and in a ci pipeline to validate kustomize overlays early.

It builds a kustomization overlay similar how the behaviour of the kustomize-controller is.
The built manifests are dumped to stdout (or to the configured output).
While this is great the big feature is that it also includes all manifests templated from each HelmRelease discovered within the kustomize build.

Like for a flux2 kustomization it automatically creates the kustomize.yaml if non exists.

* Tests if a folder recursively can be kustomized
* Templates all HelmReleases from the configured source
* Supports HelmRelease in-line values, ConfigMaps and postRender patches

The built manifests can be used for further tests like kubeconform tests, kyverno checks and other tooling.

### Usage

```
flux-build path/to/kustomize
```

### Arguments

| Flag  | Env | Default | Description |
| ------------- | ------------- | ------------- | ------------- |
| ``  | `PATHS`  | `` | **REQUIRED**: One or more paths comma separated to kustomize |
| `--workers`  | `WORKERS`  | `1` | Workers used to template the HelmReleases. Greatly improves speed if there are many HelmReleases |
| `--fail-fast`  | `FAIL_FAST` | `false` | Exit early if an error occured |
| `--allow-failure`  | `ALLOW_FAILURE` | `false` | Do not exit > 0 if an error occured |
| `--cache-dir`  | `CACHE_DIR`  | `` | Cache directory (for repositorieries, charts) |
| `--api-versions` | `API_VERSIONS` | Kubernetes api versions used for Capabilities.APIVersions |
| `--kube-version`  | `KUBE_VERSION` | `1.27.0` | Kubernetes version (Some helm charts validate manifests against a specific kubernetes version) |
| `--output`  | `OUTPUT` | `/dev/stdout` | Path to output file |


## Github Action

This app works also great on CI, in fact this was the original reason why it was created.

### Example usage

```yaml
name: flux-build
on:
- pull_request

jobs:
  build:
    strategy:
      matrix:
        cluster: [staging, production]

    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@24cb9080177205b6e8c946b17badbe402adc938f # v3.4.0
    - uses: docker://ghcr.io/doodlescheduling/flux-build:v0
      env:
        PATHS: ./${{ matrix.cluster }}
        OUTPUT: /dev/null
```


### Advanced example

While a simple gitops pipeline just verifies if kustomizations can be built and HelmReleases installed a more advanced pipeline
includes follow-up validations like kyverno tests, kubeval validations or kubeaudit tests.

```yaml
name: flux-build
on:
- pull_request

jobs:
  build:
    strategy:
      matrix:
        cluster: [staging, production]

    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@24cb9080177205b6e8c946b17badbe402adc938f # v3.4.0
    - uses: docker://ghcr.io/doodlescheduling/flux-build:v0
      env:
        PATHS: ./${{ matrix.cluster }}
        WORKERS: "50"
        OUTPUT: ./build.yaml
    - name: Setup kubeconform
      shell: bash
      run: |
        curl -L -v --fail https://github.com/yannh/kubeconform/releases/download/v0.6.1/kubeconform-linux-amd64.tar.gz -o kubeconform.tgz
        tar xvzf kubeconform.tgz
        sudo mv kubeconform /usr/bin/
    - name: Setup openapi2jsonschema
      shell: bash
      run: |
        curl -L -v --fail https://raw.githubusercontent.com/yannh/kubeconform/v0.6.2/scripts/openapi2jsonschema.py -o openapi2jsonschema.py
        sudo mv openapi2jsonschema.py /usr/bin/openapi2jsonschema
        sudo chmod +x /usr/bin/openapi2jsonschema
    - name: Setup yq
      uses: chrisdickinson/setup-yq@3d931309f27270ebbafd53f2daee773a82ea1822 #v1.0.1
      with:
        yq-version: v4.24.5
    - name: Convert CRD to json schemas
      shell: bash
      run: |
        echo "openapi2jsonschema ./build.yaml"
        mkdir "schemas"
        cat $m | yq -e 'select(.kind == "CustomResourceDefinition")' > schemas/crds.yaml
        pip install pyyaml
        openapi2jsonschema schemas/*.yaml
    - name: Run conform
      shell: bash
      env: 
        KUBERNETES_VERSION: "${{ inputs.kubernetes-version }}"
      run: |
        echo "kubeconform $m"
        cat ./build.yaml | kubeconform -kubernetes-version $KUBERNETES_VERSION -schema-location default -schema-location "schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json" --skip CustomResourceDefinition,APIService --strict --summary
    - name: Setup kyverno
      shell: bash
      run: |
        curl -LO --fail https://github.com/kyverno/kyverno/releases/download/v1.7.2/kyverno-cli_v1.7.2_linux_x86_64.tar.gz
        tar -xvf kyverno-cli_v1.7.2_linux_x86_64.tar.gz
        sudo cp kyverno /usr/local/bin/
    - name: Test kyverno policies
      shell: bash
      run: |
        kyverno apply kyverno-policies -r ./build.yaml
```

## License notice

Many internal packages have been cloned from [source-controller](https://github.com/fluxcd/source-controller) and [helm-controller](https://github.com/fluxcd/helm-controller) to achive the same functionilty for this
action as at controller runtime.

Please see upstream [license](https://github.com/fluxcd/source-controller/blob/main/LICENSE).
