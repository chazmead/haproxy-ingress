/*
Copyright 2019 The HAProxy Ingress Controller Authors.

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

package ingress

import (
	"strings"
	"testing"

	"github.com/kylelemons/godebug/diff"
	yaml "gopkg.in/yaml.v2"
	api "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"

	ing_helper "github.com/jcmoraisjr/haproxy-ingress/pkg/converters/ingress/helper_test"
	ingtypes "github.com/jcmoraisjr/haproxy-ingress/pkg/converters/ingress/types"
	"github.com/jcmoraisjr/haproxy-ingress/pkg/haproxy"
	hatypes "github.com/jcmoraisjr/haproxy-ingress/pkg/haproxy/types"
	types_helper "github.com/jcmoraisjr/haproxy-ingress/pkg/types/helper_test"
)

/* * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * *
 *
 *  CORE INGRESS
 *
 * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * */

func TestSyncSvcNotFound(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "notfound:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths: []`)

	c.compareConfigDefaultBack(`
id: system_default_8080
endpoints:
- ip: 172.17.0.99
  port: 8080`)

	c.compareLogging(`
WARN skipping backend config of ingress 'default/echo': service not found: 'default/notfound'`)
}

func TestSyncDefaultSvcNotFound(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.cache.SvcList = []*api.Service{}
	c.createSvc1Auto()
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080`)

	c.compareConfigDefaultBack(`[]`)

	c.compareLogging(`
ERROR error reading default service: service not found: 'system/default'`)
}

func TestSyncSingle(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo", "8080", "172.17.0.11,172.17.0.28")
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  - ip: 172.17.0.28
    port: 8080`)
}

func TestSyncReuseBackend(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo", "8080", "172.17.0.10,172.17.0.11")
	c.Sync(
		c.createIng1("default/ing1", "svc1.example.com", "/", "echo:8080"),
		c.createIng1("default/ing2", "svc2.example.com", "/app", "echo:8080"),
	)

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.10
    port: 8080
  - ip: 172.17.0.11
    port: 8080`)
}

func TestSyncReuseFrontend(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.21")
	c.createSvc1("default/echo2", "8080", "172.17.0.22,172.17.0.23")
	c.Sync(
		c.createIng1("default/echo1", "echo.example.com", "/path1", "echo1:8080"),
		c.createIng1("default/echo2", "echo.example.com", "/path2", "echo2:8080"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /path2
    backend: default_echo2_8080
  - path: /path1
    backend: default_echo1_8080`)
}

func TestSyncNoEndpoint(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo", "8080", "")
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareConfigBack(`
- id: default_echo_8080`)
}

func TestSyncInvalidEndpoint(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	delete(c.cache.EpList, "default/echo")
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareConfigBack(`
- id: default_echo_8080`)

	c.compareLogging(`
ERROR error adding endpoints of service 'default/echo': could not find endpoints for service 'default/echo'`)
}

func TestSyncRootPathLast(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(
		c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"),
		c.createIng1("default/echo", "echo.example.com", "/app", "echo:8080"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  - path: /
    backend: default_echo_8080`)
}

func TestSyncFrontendSorted(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.createSvc1("default/echo3", "8080", "172.17.0.13")
	c.Sync(
		c.createIng1("default/echo1", "echo-B.example.com", "/", "echo1:8080"),
		c.createIng1("default/echo2", "echo-A.example.com", "/", "echo2:8080"),
		c.createIng1("default/echo3", "echo-C.example.com", "/", "echo3:8080"),
	)

	c.compareConfigFront(`
- hostname: echo-A.example.com
  paths:
  - path: /
    backend: default_echo2_8080
- hostname: echo-B.example.com
  paths:
  - path: /
    backend: default_echo1_8080
- hostname: echo-C.example.com
  paths:
  - path: /
    backend: default_echo3_8080`)
}

func TestSyncBackendSorted(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo3", "8080", "172.17.0.13")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.Sync(
		c.createIng1("default/echo2", "echo.example.com", "/app2", "echo2:8080"),
		c.createIng1("default/echo1", "echo.example.com", "/app1", "echo1:8080"),
		c.createIng1("default/echo3", "echo.example.com", "/app3", "echo3:8080"),
	)

	c.compareConfigBack(`
- id: default_echo1_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
- id: default_echo2_8080
  endpoints:
  - ip: 172.17.0.12
    port: 8080
- id: default_echo3_8080
  endpoints:
  - ip: 172.17.0.13
    port: 8080`)
}

func TestSyncRedeclarePath(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.Sync(
		c.createIng1("default/echo1", "echo.example.com", "/p1", "echo1:8080"),
		c.createIng1("default/echo1", "echo.example.com", "/p1", "echo2:8080"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /p1
    backend: default_echo1_8080`)

	c.compareConfigBack(`
- id: default_echo1_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080`)

	c.compareLogging(`
WARN skipping redeclared path '/p1' of ingress 'default/echo1'`)
}

func TestSyncTLSDefault(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIngTLS1("default/echo", "echo.example.com", "/", "echo:8080", ""))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/tls-default.pem`)
}

func TestSyncTLSSecretNotFound(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIngTLS1("default/echo", "echo.example.com", "/", "echo:8080", "ing-tls"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/tls-default.pem`)

	c.compareLogging(`
WARN using default certificate due to an error reading secret 'default/ing-tls': secret not found: 'default/ing-tls'`)
}

func TestSyncTLSCustom(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.createSecretTLS1("default/tls-echo")
	c.Sync(c.createIngTLS1("default/echo", "echo.example.com", "/", "echo:8080", "tls-echo"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/default/tls-echo.pem`)
}

func TestSyncRedeclareTLS(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.createSecretTLS1("default/tls-echo1")
	c.createSecretTLS1("default/tls-echo2")
	c.Sync(c.createIngTLS1("default/echo1", "echo.example.com", "/", "echo:8080", "tls-echo1:echo.example.com;tls-echo2:echo.example.com"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/default/tls-echo1.pem`)

	c.compareLogging(`
WARN skipping TLS secret 'tls-echo2' of ingress 'default/echo1': TLS of host 'echo.example.com' was already assigned`)
}

func TestSyncRedeclareSameTLS(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.createSecretTLS1("default/tls-echo1")
	c.Sync(
		c.createIngTLS1("default/echo1", "echo.example.com", "/", "echo:8080", "tls-echo1:echo.example.com"),
		c.createIngTLS1("default/echo2", "echo.example.com", "/app", "echo:8080", "tls-echo1:echo.example.com"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/default/tls-echo1.pem`)
}

func TestSyncRedeclareTLSDefaultFirst(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.createSecretTLS1("default/tls-echo1")
	c.Sync(
		c.createIngTLS1("default/echo1", "echo.example.com", "/", "echo:8080", ""),
		c.createIngTLS1("default/echo2", "echo.example.com", "/app", "echo:8080", "tls-echo1:echo.example.com"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/tls-default.pem`)

	c.compareLogging(`
WARN skipping TLS secret 'tls-echo1' of ingress 'default/echo2': TLS of host 'echo.example.com' was already assigned`)
}

func TestSyncRedeclareTLSCustomFirst(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.createSecretTLS1("default/tls-echo1")
	c.Sync(
		c.createIngTLS1("default/echo1", "echo.example.com", "/", "echo:8080", "tls-echo1:echo.example.com"),
		c.createIngTLS1("default/echo2", "echo.example.com", "/app", "echo:8080", ""),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  - path: /
    backend: default_echo_8080
  tls:
    tlsfilename: /tls/default/tls-echo1.pem`)

	c.compareLogging(`
WARN skipping default TLS secret of ingress 'default/echo2': TLS of host 'echo.example.com' was already assigned`)
}

func TestSyncNoDefaultNoTLS(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.cache.SecretTLSPath = map[string]string{}
	c.createSvc1Auto()
	c.Sync(c.createIngTLS1("default/echo", "echo.example.com", "/", "echo:8080", ""))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareLogging(`
WARN skipping default TLS secret of ingress 'default/echo': secret not found: 'system/ingress-default'`)
}

func TestSyncNoDefaultInvalidTLS(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.cache.SecretTLSPath = map[string]string{}
	c.createSvc1Auto()
	c.Sync(c.createIngTLS1("default/echo", "echo.example.com", "/", "echo:8080", "tls-invalid"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)

	c.compareLogging(`
WARN skipping TLS secret 'tls-invalid' of ingress 'default/echo': failed to use custom and default certificate. custom: secret not found: 'default/tls-invalid'; default: secret not found: 'system/ingress-default'`)
}

func TestSyncRootPathDefault(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/app", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080`)
}

func TestSyncPathEmpty(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng1("default/echo", "echo.example.com", "", "echo:8080"))

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080`)
}

func TestSyncBackendDefault(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng2("default/echo", "echo:8080"))

	c.compareConfigDefaultFront(`
hostname: '*'
paths:
- path: /
  backend: default_echo_8080`)

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080`)
}

func TestSyncBackendSvcNotFound(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng2("default/echo", "notfound:8080"))

	c.compareConfigFront(`[]`)
	c.compareConfigBack(`[]`)

	c.compareLogging(`
WARN skipping default backend of ingress 'default/echo': service not found: 'default/notfound'`)
}

func TestSyncBackendReuseDefaultSvc(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.Sync(c.createIng1("system/defbackend", "default.example.com", "/app", "default:8080"))

	c.compareConfigFront(`
- hostname: default.example.com
  paths:
  - path: /app
    backend: system_default_8080`)

	c.compareConfigBack(`[]`)

	c.compareConfigDefaultBack(`
id: system_default_8080
endpoints:
- ip: 172.17.0.99
  port: 8080`)
}

func TestSyncDefaultBackendReusedPath1(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.Sync(
		c.createIng1("default/echo1", "'*'", "/", "echo1:8080"),
		c.createIng2("default/echo2", "echo2:8080"),
	)

	c.compareConfigDefaultFront(`
hostname: '*'
paths:
- path: /
  backend: default_echo1_8080`)

	c.compareConfigBack(`
- id: default_echo1_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080`)

	c.compareLogging(`
WARN skipping default backend of ingress 'default/echo2': path / was already defined on default host`)
}

func TestSyncDefaultBackendReusedPath2(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.Sync(
		c.createIng2("default/echo1", "echo1:8080"),
		c.createIng1("default/echo2", "'*'", "/", "echo2:8080"),
	)

	c.compareConfigDefaultFront(`
hostname: '*'
paths:
- path: /
  backend: default_echo1_8080`)

	c.compareConfigBack(`
- id: default_echo1_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080`)

	c.compareLogging(`
WARN skipping redeclared path '/' of ingress 'default/echo2'`)
}

func TestSyncEmptyHTTP(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.Sync(c.createIng3("default/echo"))
	c.compareConfigFront(`[]`)
}

func TestSyncEmptyHost(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng1("default/echo", "", "/", "echo:8080"))

	c.compareConfigDefaultFront(`
hostname: '*'
paths:
- path: /
  backend: default_echo_8080`)
}

func TestSyncMultiNamespace(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("ns1/echo", "8080", "172.17.0.11")
	c.createSvc1("ns2/echo", "8080", "172.17.0.12")

	c.Sync(
		c.createIng1("ns1/echo", "echo.example.com", "/app1", "echo:8080"),
		c.createIng1("ns2/echo", "echo.example.com", "/app2", "echo:8080"),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app2
    backend: ns2_echo_8080
  - path: /app1
    backend: ns1_echo_8080`)

	c.compareConfigBack(`
- id: ns1_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
- id: ns2_echo_8080
  endpoints:
  - ip: 172.17.0.12
    port: 8080`)
}

/* * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * *
 *
 *  ANNOTATIONS
 *
 * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * */

func TestSyncAnnFront(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(
		c.createIng1Ann("default/echo", "echo.example.com", "/", "echo:8080", map[string]string{
			"ingress.kubernetes.io/app-root": "/app",
		}),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /
    backend: default_echo_8080
  rootredirect: /app`)
}

func TestSyncAnnFrontsConflict(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(
		c.createIng1Ann("default/echo1", "echo.example.com", "/", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "1s",
		}),
		c.createIng1Ann("default/echo2", "echo.example.com", "/app", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "2s",
		}),
	)

	c.compareConfigFront(`
- hostname: echo.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  - path: /
    backend: default_echo_8080
  timeout:
    client: 1s`)

	c.compareLogging(`
INFO skipping frontend annotation(s) from ingress 'default/echo2' due to conflict: [timeout-client]`)
}

func TestSyncAnnFronts(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(
		c.createIng1Ann("default/echo1", "echo1.example.com", "/app1", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "1s",
		}),
		c.createIng1Ann("default/echo2", "echo2.example.com", "/app2", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "2s",
		}),
	)

	c.compareConfigFront(`
- hostname: echo1.example.com
  paths:
  - path: /app1
    backend: default_echo_8080
  timeout:
    client: 1s
- hostname: echo2.example.com
  paths:
  - path: /app2
    backend: default_echo_8080
  timeout:
    client: 2s`)
}

func TestSyncAnnFrontDefault(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.SyncDef(map[string]string{"timeout-client": "1s"},
		c.createIng1Ann("default/echo1", "echo1.example.com", "/app", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "2s",
		}),
		c.createIng1Ann("default/echo2", "echo2.example.com", "/app", "echo:8080", map[string]string{
			"ingress.kubernetes.io/timeout-client": "1s",
		}),
		c.createIng1Ann("default/echo3", "echo3.example.com", "/app", "echo:8080", map[string]string{}),
	)

	c.compareConfigFront(`
- hostname: echo1.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  timeout:
    client: 2s
- hostname: echo2.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  timeout:
    client: 1s
- hostname: echo3.example.com
  paths:
  - path: /app
    backend: default_echo_8080
  timeout:
    client: 1s`)
}

func TestSyncAnnBack(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1Auto()
	c.Sync(c.createIng1Ann("default/echo", "echo.example.com", "/", "echo:8080", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	}))

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  balancealgorithm: leastconn`)
}

func TestSyncAnnBackSvc(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1AutoAnn(map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.Sync(c.createIng1("default/echo", "echo.example.com", "/", "echo:8080"))

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  balancealgorithm: leastconn`)
}

func TestSyncAnnBackSvcIngConflict(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1AutoAnn(map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.Sync(c.createIng1Ann("default/echo", "echo.example.com", "/", "echo:8080", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "first",
	}))

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  balancealgorithm: leastconn`)

	c.compareLogging(`
INFO skipping backend 'default/echo:8080' annotation(s) from ingress 'default/echo' due to conflict: [balance-algorithm]`)
}

func TestSyncAnnBacksSvcIng(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1AutoAnn(map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.Sync(c.createIng1Ann("default/echo", "echo.example.com", "/", "echo:8080", map[string]string{
		"ingress.kubernetes.io/maxconn-server": "10",
	}))

	c.compareConfigBack(`
- id: default_echo_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  balancealgorithm: leastconn
  maxconnserver: 10`)
}

func TestSyncAnnBackDefault(t *testing.T) {
	c := setup(t)
	defer c.teardown()

	c.createSvc1("default/echo1", "8080", "172.17.0.11")
	c.createSvc1("default/echo2", "8080", "172.17.0.12")
	c.createSvc1("default/echo3", "8080", "172.17.0.13")
	c.createSvc1Ann("default/echo4", "8080", "172.17.0.14", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.createSvc1Ann("default/echo5", "8080", "172.17.0.15", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.createSvc1Ann("default/echo6", "8080", "172.17.0.16", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "leastconn",
	})
	c.createSvc1Ann("default/echo7", "8080", "172.17.0.17", map[string]string{
		"ingress.kubernetes.io/balance-algorithm": "roundrobin",
	})
	c.SyncDef(map[string]string{"balance-algorithm": "roundrobin"},
		c.createIng1Ann("default/echo1", "echo.example.com", "/app1", "echo1:8080", map[string]string{
			"ingress.kubernetes.io/balance-algorithm": "leastconn",
		}),
		c.createIng1Ann("default/echo2", "echo.example.com", "/app2", "echo2:8080", map[string]string{
			"ingress.kubernetes.io/balance-algorithm": "roundrobin",
		}),
		c.createIng1Ann("default/echo3", "echo.example.com", "/app3", "echo3:8080", map[string]string{}),
		c.createIng1Ann("default/echo4", "echo.example.com", "/app4", "echo4:8080", map[string]string{}),
		c.createIng1Ann("default/echo5", "echo.example.com", "/app5", "echo5:8080", map[string]string{
			"ingress.kubernetes.io/balance-algorithm": "first",
		}),
		c.createIng1Ann("default/echo6", "echo.example.com", "/app6", "echo6:8080", map[string]string{
			"ingress.kubernetes.io/balance-algorithm": "roundrobin",
		}),
		c.createIng1Ann("default/echo7", "echo.example.com", "/app7", "echo7:8080", map[string]string{
			"ingress.kubernetes.io/balance-algorithm": "leastconn",
		}),
	)

	c.compareConfigBack(`
- id: default_echo1_8080
  endpoints:
  - ip: 172.17.0.11
    port: 8080
  balancealgorithm: leastconn
- id: default_echo2_8080
  endpoints:
  - ip: 172.17.0.12
    port: 8080
  balancealgorithm: roundrobin
- id: default_echo3_8080
  endpoints:
  - ip: 172.17.0.13
    port: 8080
  balancealgorithm: roundrobin
- id: default_echo4_8080
  endpoints:
  - ip: 172.17.0.14
    port: 8080
  balancealgorithm: leastconn
- id: default_echo5_8080
  endpoints:
  - ip: 172.17.0.15
    port: 8080
  balancealgorithm: leastconn
- id: default_echo6_8080
  endpoints:
  - ip: 172.17.0.16
    port: 8080
  balancealgorithm: leastconn
- id: default_echo7_8080
  endpoints:
  - ip: 172.17.0.17
    port: 8080
  balancealgorithm: leastconn`)

	c.compareLogging(`
INFO skipping backend 'default/echo5:8080' annotation(s) from ingress 'default/echo5' due to conflict: [balance-algorithm]`)
}

/* * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * *
 *
 *  BUILDERS
 *
 * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * * */

type testConfig struct {
	t       *testing.T
	decode  func(data []byte, defaults *schema.GroupVersionKind, into runtime.Object) (runtime.Object, *schema.GroupVersionKind, error)
	hconfig haproxy.Config
	logger  *types_helper.LoggerMock
	cache   *ing_helper.CacheMock
	updater *ing_helper.UpdaterMock
}

func setup(t *testing.T) *testConfig {
	logger := &types_helper.LoggerMock{
		Logging: []string{},
		T:       t,
	}
	c := &testConfig{
		t:       t,
		decode:  scheme.Codecs.UniversalDeserializer().Decode,
		hconfig: haproxy.CreateInstance(logger, haproxy.InstanceOptions{}).Config(),
		cache: &ing_helper.CacheMock{
			SvcList: []*api.Service{},
			EpList:  map[string]*api.Endpoints{},
			SecretTLSPath: map[string]string{
				"system/ingress-default": "/tls/tls-default.pem",
			},
		},
		logger: logger,
	}
	c.createSvc1("system/default", "8080", "172.17.0.99")
	return c
}

func (c *testConfig) teardown() {
	c.compareLogging("")
}

func (c *testConfig) Sync(ing ...*extensions.Ingress) {
	c.SyncDef(map[string]string{}, ing...)
}

func (c *testConfig) SyncDef(config map[string]string, ing ...*extensions.Ingress) {
	conv := NewIngressConverter(
		&ingtypes.ConverterOptions{
			Cache:            c.cache,
			Logger:           c.logger,
			DefaultBackend:   "system/default",
			DefaultSSLSecret: "system/ingress-default",
			AnnotationPrefix: "ingress.kubernetes.io",
		},
		c.hconfig,
		config,
	).(*converter)
	conv.updater = c.updater
	conv.globalConfig = mergeConfig(&ingtypes.Config{}, config)
	conv.Sync(ing)
}

func (c *testConfig) createSvc1Auto() *api.Service {
	return c.createSvc1("default/echo", "8080", "172.17.0.11")
}

func (c *testConfig) createSvc1AutoAnn(ann map[string]string) *api.Service {
	svc := c.createSvc1Auto()
	svc.SetAnnotations(ann)
	return svc
}

func (c *testConfig) createSvc1Ann(name, port, endpoints string, ann map[string]string) *api.Service {
	svc := c.createSvc1(name, port, endpoints)
	svc.SetAnnotations(ann)
	return svc
}

func (c *testConfig) createSvc1(name, port, endpoints string) *api.Service {
	sname := strings.Split(name, "/")

	svc := c.createObject(`
apiVersion: v1
kind: Service
metadata:
  name: ` + sname[1] + `
  namespace: ` + sname[0] + `
spec:
  ports:
  - port: ` + port + `
    targetPort: ` + port).(*api.Service)

	c.cache.SvcList = append(c.cache.SvcList, svc)

	ep := c.createObject(`
apiVersion: v1
kind: Endpoints
metadata:
  name: ` + sname[1] + `
  namespace: ` + sname[0] + `
subsets:
- addresses: []
  ports:
  - port: ` + port + `
    protocol: TCP`).(*api.Endpoints)

	addr := []api.EndpointAddress{}
	for _, e := range strings.Split(endpoints, ",") {
		if e != "" {
			target := &api.ObjectReference{
				Kind:      "Pod",
				Name:      sname[1] + "-xxxxx",
				Namespace: sname[0],
			}
			addr = append(addr, api.EndpointAddress{IP: e, TargetRef: target})
		}
	}
	ep.Subsets[0].Addresses = addr
	c.cache.EpList[name] = ep

	return svc
}

func (c *testConfig) createSecretTLS1(secretName string) {
	c.cache.SecretTLSPath[secretName] = "/tls/" + secretName + ".pem"
}

func (c *testConfig) createIng1(name, hostname, path, service string) *extensions.Ingress {
	sname := strings.Split(name, "/")
	sservice := strings.Split(service, ":")
	return c.createObject(`
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: ` + sname[1] + `
  namespace: ` + sname[0] + `
spec:
  rules:
  - host: ` + hostname + `
    http:
      paths:
      - path: ` + path + `
        backend:
          serviceName: ` + sservice[0] + `
          servicePort: ` + sservice[1]).(*extensions.Ingress)
}

func (c *testConfig) createIng1Ann(name, hostname, path, service string, ann map[string]string) *extensions.Ingress {
	ing := c.createIng1(name, hostname, path, service)
	ing.SetAnnotations(ann)
	return ing
}

func (c *testConfig) createIng2(name, service string) *extensions.Ingress {
	sname := strings.Split(name, "/")
	sservice := strings.Split(service, ":")
	return c.createObject(`
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: ` + sname[1] + `
  namespace: ` + sname[0] + `
spec:
  backend:
    serviceName: ` + sservice[0] + `
    servicePort: ` + sservice[1]).(*extensions.Ingress)
}

func (c *testConfig) createIng3(name string) *extensions.Ingress {
	sname := strings.Split(name, "/")
	return c.createObject(`
apiVersion: extensions/v1beta1
kind: Ingress
metadata:
  name: ` + sname[1] + `
  namespace: ` + sname[0] + `
spec:
  rules:
  - http:`).(*extensions.Ingress)
}

func (c *testConfig) createIngTLS1(name, hostname, path, service, secretHostName string) *extensions.Ingress {
	tls := []extensions.IngressTLS{}
	for _, secret := range strings.Split(secretHostName, ";") {
		ssecret := strings.Split(secret, ":")
		hosts := []string{}
		if len(ssecret) > 1 {
			for _, host := range strings.Split(ssecret[1], ",") {
				hosts = append(hosts, host)
			}
		}
		if len(hosts) == 0 {
			hosts = []string{hostname}
		}
		tls = append(tls, extensions.IngressTLS{
			Hosts:      hosts,
			SecretName: ssecret[0],
		})
	}
	ing := c.createIng1(name, hostname, path, service)
	ing.Spec.TLS = tls
	return ing
}

func (c *testConfig) createObject(cfg string) runtime.Object {
	obj, _, err := c.decode([]byte(cfg), nil, nil)
	if err != nil {
		c.t.Errorf("error decoding object: %v", err)
		return nil
	}
	return obj
}

func _yamlMarshal(in interface{}) string {
	out, _ := yaml.Marshal(in)
	return string(out)
}

func (c *testConfig) compareText(actual, expected string) {
	txt1 := "\n" + strings.Trim(expected, "\n")
	txt2 := "\n" + strings.Trim(actual, "\n")
	if txt1 != txt2 {
		c.t.Error(diff.Diff(txt1, txt2))
	}
}

type (
	pathMock struct {
		Path      string
		BackendID string `yaml:"backend"`
	}
	timeoutMock struct {
		Client string `yaml:",omitempty"`
	}
	tlsMock struct {
		TLSFilename string `yaml:",omitempty"`
	}
	frontendMock struct {
		Hostname     string
		Paths        []pathMock
		RootRedirect string      `yaml:",omitempty"`
		Timeout      timeoutMock `yaml:",omitempty"`
		TLS          tlsMock     `yaml:",omitempty"`
	}
)

func convertFrontend(hafronts ...*hatypes.Frontend) []frontendMock {
	frontends := []frontendMock{}
	for _, f := range hafronts {
		paths := []pathMock{}
		for _, p := range f.Paths {
			paths = append(paths, pathMock{Path: p.Path, BackendID: p.BackendID})
		}
		frontends = append(frontends, frontendMock{
			Hostname:     f.Hostname,
			Paths:        paths,
			RootRedirect: f.RootRedirect,
			Timeout:      timeoutMock{Client: f.Timeout.Client},
			TLS:          tlsMock{TLSFilename: f.TLS.TLSFilename},
		})
	}
	return frontends
}

func (c *testConfig) compareConfigFront(expected string) {
	c.compareText(_yamlMarshal(convertFrontend(c.hconfig.Frontends()...)), expected)
}

func (c *testConfig) compareConfigDefaultFront(expected string) {
	frontend := c.hconfig.DefaultFrontend()
	if frontend != nil {
		c.compareText(_yamlMarshal(convertFrontend(frontend)[0]), expected)
	} else {
		c.compareText("[]", expected)
	}
}

type (
	endpointMock struct {
		IP   string
		Port int
	}
	backendMock struct {
		ID               string
		Endpoints        []endpointMock `yaml:",omitempty"`
		BalanceAlgorithm string         `yaml:",omitempty"`
		MaxconnServer    int            `yaml:",omitempty"`
	}
)

func convertBackend(habackends ...*hatypes.Backend) []backendMock {
	backends := []backendMock{}
	for _, b := range habackends {
		endpoints := []endpointMock{}
		for _, e := range b.Endpoints {
			endpoints = append(endpoints, endpointMock{IP: e.IP, Port: e.Port})
		}
		backends = append(backends, backendMock{
			ID:               b.ID,
			Endpoints:        endpoints,
			BalanceAlgorithm: b.BalanceAlgorithm,
			MaxconnServer:    b.MaxconnServer,
		})
	}
	return backends
}

func (c *testConfig) compareConfigBack(expected string) {
	c.compareText(_yamlMarshal(convertBackend(c.hconfig.Backends()...)), expected)
}

func (c *testConfig) compareConfigDefaultBack(expected string) {
	backend := c.hconfig.DefaultBackend()
	if backend != nil {
		c.compareText(_yamlMarshal(convertBackend(backend)[0]), expected)
	} else {
		c.compareText("[]", expected)
	}
}

func (c *testConfig) compareLogging(expected string) {
	c.compareText(strings.Join(c.logger.Logging, "\n"), expected)
	c.logger.Logging = []string{}
}
