/*
Copyright 2022 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package registry

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"

	"helm.sh/helm/v4/pkg/helmpath"
	"helm.sh/helm/v4/pkg/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/fluxcd/pkg/oci"
)

var (
	// userAgent is the User-Agent header value sent with each request to an OCI registry
	// through the Helm/ORAS client. It extends the pkg/oci.UserAgent ("flux/v2") following
	// its format "<tool>/<version>".
	userAgent = fmt.Sprintf("%s/helm/v4/oras/v2", oci.UserAgent)
)

// NewClient creates a new OCI registry client with the provided options.
// If no explicit creds were resolved (no HelmRepository Secret/OIDC auth), it
// falls back to the local Helm and Docker credential stores (populated by
// e.g. `helm registry login` or `docker login`), matching the behavior of
// helm's own registry.NewClient default (unauthenticated) client.
func NewClient(creds auth.CredentialFunc, tlsConfig *tls.Config, insecureHTTP bool) (*registry.Client, error) {
	baseTransport := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConfig != nil {
		baseTransport.TLSClientConfig = tlsConfig
	}

	if creds == nil {
		var err error
		creds, err = localCredentials()
		if err != nil {
			return nil, err
		}
	}

	client := auth.Client{
		Client: &http.Client{
			// We use the oras retry transport here to keep consistent with oras behavior.
			Transport: retry.NewTransport(baseTransport),
		},
		Header: http.Header{
			"User-Agent": {userAgent},
		},
		Credential: creds,
	}
	opts := []registry.ClientOption{
		registry.ClientOptWriter(io.Discard),
		registry.ClientOptAuthorizer(client),
	}
	if insecureHTTP {
		opts = append(opts, registry.ClientOptPlainHTTP())
	}
	return registry.NewClient(opts...)
}

// localCredentials returns a credential function backed by the local Helm
// registry credential store (~/.config/helm/registry/config.json), falling
// back to the Docker credential store (~/.docker/config.json, including
// credential helpers) when no matching entry is found in the former.
func localCredentials() (auth.CredentialFunc, error) {
	storeOpts := credentials.StoreOptions{
		AllowPlaintextPut:        true,
		DetectDefaultNativeStore: true,
	}

	store, err := credentials.NewStore(helmpath.ConfigPath(registry.CredentialsFileBasename), storeOpts)
	if err != nil {
		return nil, err
	}

	if dockerStore, err := credentials.NewStoreFromDocker(storeOpts); err == nil {
		return credentials.Credential(credentials.NewStoreWithFallbacks(store, dockerStore)), nil
	}

	return credentials.Credential(store), nil
}
