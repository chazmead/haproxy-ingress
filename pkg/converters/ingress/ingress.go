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
	"fmt"
	"strings"

	api "k8s.io/api/core/v1"
	extensions "k8s.io/api/extensions/v1beta1"

	"github.com/jcmoraisjr/haproxy-ingress/pkg/converters/ingress/annotations"
	ingtypes "github.com/jcmoraisjr/haproxy-ingress/pkg/converters/ingress/types"
	"github.com/jcmoraisjr/haproxy-ingress/pkg/converters/ingress/utils"
	"github.com/jcmoraisjr/haproxy-ingress/pkg/haproxy"
	hatypes "github.com/jcmoraisjr/haproxy-ingress/pkg/haproxy/types"
	"github.com/jcmoraisjr/haproxy-ingress/pkg/types"
)

// Config ...
type Config interface {
	Sync(ingress []*extensions.Ingress)
}

// NewIngressConverter ...
func NewIngressConverter(options *ingtypes.ConverterOptions, haproxy haproxy.Config, globalConfig map[string]string) Config {
	c := &converter{
		haproxy:             haproxy,
		options:             options,
		logger:              options.Logger,
		cache:               options.Cache,
		updater:             annotations.NewUpdater(haproxy, options.Cache, options.Logger),
		globalConfig:        mergeConfig(createDefaults(), globalConfig),
		frontendAnnotations: map[string]*ingtypes.FrontendAnnotations{},
		backendAnnotations:  map[string]*ingtypes.BackendAnnotations{},
	}
	if backend, err := c.addBackend(options.DefaultBackend, 0, &ingtypes.BackendAnnotations{}); err == nil {
		haproxy.ConfigDefaultBackend(backend)
	} else {
		c.logger.Error("error reading default service: %v", err)
	}
	return c
}

type converter struct {
	haproxy             haproxy.Config
	options             *ingtypes.ConverterOptions
	logger              types.Logger
	cache               ingtypes.Cache
	updater             annotations.Updater
	globalConfig        *ingtypes.Config
	frontendAnnotations map[string]*ingtypes.FrontendAnnotations
	backendAnnotations  map[string]*ingtypes.BackendAnnotations
}

func (c *converter) Sync(ingress []*extensions.Ingress) {
	for _, ing := range ingress {
		c.syncIngress(ing)
	}
	c.syncAnnotations()
}

func (c *converter) syncIngress(ing *extensions.Ingress) {
	fullIngName := fmt.Sprintf("%s/%s", ing.Namespace, ing.Name)
	ingFrontAnn, ingBackAnn := c.readAnnotations(&ingtypes.Source{
		Namespace: ing.Namespace,
		Name:      ing.Name,
		Type:      "ingress",
	}, ing.Annotations)
	if ing.Spec.Backend != nil {
		svcName, svcPort := readServiceNamePort(ing.Spec.Backend)
		err := c.addDefaultHostBackend(utils.FullQualifiedName(ing.Namespace, svcName), svcPort, ingFrontAnn, ingBackAnn)
		if err != nil {
			c.logger.Warn("skipping default backend of ingress '%s': %v", fullIngName, err)
		}
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		hostname := rule.Host
		if hostname == "" {
			hostname = "*"
		}
		frontend := c.addFrontend(hostname, ingFrontAnn)
		for _, path := range rule.HTTP.Paths {
			uri := path.Path
			if uri == "" {
				uri = "/"
			}
			if frontend.FindPath(uri) != nil {
				c.logger.Warn("skipping redeclared path '%s' of ingress '%s'", uri, fullIngName)
				continue
			}
			svcName, svcPort := readServiceNamePort(&path.Backend)
			fullSvcName := utils.FullQualifiedName(ing.Namespace, svcName)
			backend, err := c.addBackend(fullSvcName, svcPort, ingBackAnn)
			if err != nil {
				c.logger.Warn("skipping backend config of ingress '%s': %v", fullIngName, err)
				continue
			}
			frontend.AddPath(backend, uri)
			c.addHTTPPassthrough(fullSvcName, ingFrontAnn, ingBackAnn)
		}
		for _, tls := range ing.Spec.TLS {
			for _, host := range tls.Hosts {
				if host == hostname {
					tlsPath, err := c.addTLS(ing.Namespace, tls.SecretName)
					if err == nil {
						if frontend.TLS.TLSFilename == "" {
							frontend.TLS.TLSFilename = tlsPath
						} else if frontend.TLS.TLSFilename != tlsPath {
							err = fmt.Errorf("TLS of host '%s' was already assigned", frontend.Hostname)
						}
					}
					if err != nil {
						if tls.SecretName != "" {
							c.logger.Warn("skipping TLS secret '%s' of ingress '%s': %v", tls.SecretName, fullIngName, err)
						} else {
							c.logger.Warn("skipping default TLS secret of ingress '%s': %v", fullIngName, err)
						}
					}
				}
			}
		}
	}
}

func (c *converter) syncAnnotations() {
	c.updater.UpdateGlobalConfig(c.haproxy.Global(), c.globalConfig)
	for _, frontend := range c.haproxy.Frontends() {
		if ann, found := c.frontendAnnotations[frontend.Hostname]; found {
			c.updater.UpdateFrontendConfig(frontend, ann)
		}
	}
	for _, backend := range c.haproxy.Backends() {
		if ann, found := c.backendAnnotations[backend.ID]; found {
			c.updater.UpdateBackendConfig(backend, ann)
		}
	}
}

func (c *converter) addDefaultHostBackend(fullSvcName string, svcPort int, ingFrontAnn *ingtypes.FrontendAnnotations, ingBackAnn *ingtypes.BackendAnnotations) error {
	if fr := c.haproxy.FindFrontend("*"); fr != nil {
		if fr.FindPath("/") != nil {
			return fmt.Errorf("path / was already defined on default host")
		}
	}
	backend, err := c.addBackend(fullSvcName, svcPort, ingBackAnn)
	if err != nil {
		return err
	}
	frontend := c.addFrontend("*", ingFrontAnn)
	frontend.AddPath(backend, "/")
	return nil
}

func (c *converter) addFrontend(host string, ingAnn *ingtypes.FrontendAnnotations) *hatypes.Frontend {
	frontend := c.haproxy.AcquireFrontend(host)
	if ann, found := c.frontendAnnotations[frontend.Hostname]; found {
		skipped, _ := utils.UpdateStruct(c.globalConfig.ConfigDefaults, ingAnn, ann)
		if len(skipped) > 0 {
			c.logger.Info("skipping frontend annotation(s) from %v due to conflict: %v", ingAnn.Source, skipped)
		}
	} else {
		c.frontendAnnotations[frontend.Hostname] = ingAnn
	}
	return frontend
}

func (c *converter) addBackend(fullSvcName string, svcPort int, ingAnn *ingtypes.BackendAnnotations) (*hatypes.Backend, error) {
	svc, err := c.cache.GetService(fullSvcName)
	if err != nil {
		return nil, err
	}
	ssvcName := strings.Split(fullSvcName, "/")
	namespace := ssvcName[0]
	svcName := ssvcName[1]
	if svcPort == 0 {
		// if the port wasn't specified, take the first one
		// from the api.Service object
		// TODO named port
		svcPort = svc.Spec.Ports[0].TargetPort.IntValue()
	}
	backend := c.haproxy.AcquireBackend(namespace, svcName, svcPort)
	ann, found := c.backendAnnotations[backend.ID]
	if !found {
		// New backend, configure endpoints and svc annotations
		if err := c.addEndpoints(svc, svcPort, backend); err != nil {
			c.logger.Error("error adding endpoints of service '%s': %v", fullSvcName, err)
		}
		// Initialize with service annotations, giving precedence
		_, ann = c.readAnnotations(&ingtypes.Source{
			Namespace: namespace,
			Name:      svcName,
			Type:      "service",
		}, svc.Annotations)
		c.backendAnnotations[backend.ID] = ann
	}
	// Merging Ingress annotations
	skipped, _ := utils.UpdateStruct(c.globalConfig.ConfigDefaults, ingAnn, ann)
	if len(skipped) > 0 {
		c.logger.Info("skipping backend '%s/%s:%d' annotation(s) from %v due to conflict: %v",
			backend.Namespace, backend.Name, backend.Port, ingAnn.Source, skipped)
	}
	return backend, nil
}

func (c *converter) addHTTPPassthrough(fullSvcName string, ingFrontAnn *ingtypes.FrontendAnnotations, ingBackAnn *ingtypes.BackendAnnotations) {
	// a very specific use case of pre-parsing annotations:
	// need to add a backend if ssl-passthrough-http-port assigned
	if ingFrontAnn.SSLPassthrough && ingFrontAnn.SSLPassthroughHTTPPort != 0 {
		c.addBackend(fullSvcName, ingFrontAnn.SSLPassthroughHTTPPort, ingBackAnn)
	}
}

func (c *converter) addTLS(namespace, secretName string) (string, error) {
	defaultSecret := c.options.DefaultSSLSecret
	tlsSecretName := defaultSecret
	if secretName != "" {
		tlsSecretName = namespace + "/" + secretName
	}
	tlsPath, err := c.cache.GetTLSSecretPath(tlsSecretName)
	if err != nil {
		if tlsSecretName == defaultSecret {
			return "", err
		}
		tlsSecretErr := err
		tlsPath, err = c.cache.GetTLSSecretPath(defaultSecret)
		if err != nil {
			return "", fmt.Errorf("failed to use custom and default certificate. custom: %v; default: %v", tlsSecretErr, err)
		}
		c.logger.Warn("using default certificate due to an error reading secret '%s': %v", tlsSecretName, tlsSecretErr)
	}
	return tlsPath, nil
}

func (c *converter) addEndpoints(svc *api.Service, servicePort int, backend *hatypes.Backend) error {
	endpoints, err := c.cache.GetEndpoints(svc)
	if err != nil {
		return err
	}
	// TODO ServiceTypeExternalName
	// TODO ServiceUpstream - annotation nao documentada
	// TODO DrainSupport
	for _, subset := range endpoints.Subsets {
		for _, port := range subset.Ports {
			if int(port.Port) == servicePort && port.Protocol == api.ProtocolTCP {
				for _, addr := range subset.Addresses {
					backend.NewEndpoint(addr.IP, servicePort, addr.TargetRef.Namespace+"/"+addr.TargetRef.Name)
				}
			}
		}
	}
	return nil
}

func (c *converter) readAnnotations(source *ingtypes.Source, annotations map[string]string) (*ingtypes.FrontendAnnotations, *ingtypes.BackendAnnotations) {
	ann := make(map[string]string, len(annotations))
	prefix := c.options.AnnotationPrefix + "/"
	for annName, annValue := range annotations {
		if strings.HasPrefix(annName, prefix) {
			name := strings.TrimPrefix(annName, prefix)
			ann[name] = annValue
		}
	}
	frontAnn := &ingtypes.FrontendAnnotations{Source: *source}
	backAnn := &ingtypes.BackendAnnotations{Source: *source}
	utils.UpdateStruct(struct{}{}, c.globalConfig.ConfigDefaults, frontAnn)
	utils.UpdateStruct(struct{}{}, c.globalConfig.ConfigDefaults, backAnn)
	if err := utils.MergeMap(ann, frontAnn); err != nil {
		c.logger.Error("error merging frontend annotations from %v: %v", source, err)
	}
	if err := utils.MergeMap(ann, backAnn); err != nil {
		c.logger.Error("error merging backend annotations from %v: %v", source, err)
	}
	return frontAnn, backAnn
}

func readServiceNamePort(backend *extensions.IngressBackend) (string, int) {
	serviceName := backend.ServiceName
	servicePort := backend.ServicePort.IntValue()
	return serviceName, servicePort
}
