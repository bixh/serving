/*
Copyright 2019 The Knative Authors

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

package resources

import (
	"fmt"
	"testing"

	"knative.dev/pkg/kmeta"

	"github.com/google/go-cmp/cmp"
	"github.com/knative/serving/pkg/apis/networking"
	"github.com/knative/serving/pkg/apis/networking/v1alpha1"
	"github.com/knative/serving/pkg/reconciler/ingress/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeinformers "k8s.io/client-go/informers"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	. "knative.dev/pkg/logging/testing"
)

var testSecret = corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "secret0",
		Namespace: "knative-serving",
	},
	Data: map[string][]byte{
		"test": []byte("abcd"),
	},
}

var ci = v1alpha1.ClusterIngress{
	ObjectMeta: metav1.ObjectMeta{
		Name: "ingress",
	},
	Spec: v1alpha1.IngressSpec{
		TLS: []v1alpha1.IngressTLS{{
			Hosts:           []string{"example.com"},
			SecretName:      "secret0",
			SecretNamespace: "knative-serving",
		}},
	},
}

func TestGetSecrets(t *testing.T) {
	kubeClient := fakek8s.NewSimpleClientset()
	secretClient := kubeinformers.NewSharedInformerFactory(kubeClient, 0).Core().V1().Secrets()
	createSecret := func(secret *corev1.Secret) {
		kubeClient.CoreV1().Secrets(secret.Namespace).Create(secret)
		secretClient.Informer().GetIndexer().Add(secret)
	}

	cases := []struct {
		name     string
		secret   *corev1.Secret
		ci       *v1alpha1.ClusterIngress
		expected map[string]*corev1.Secret
		wantErr  bool
	}{{
		name:   "Get secrets successfully.",
		secret: &testSecret,
		ci:     &ci,
		expected: map[string]*corev1.Secret{
			"knative-serving/secret0": &testSecret,
		},
	}, {
		name:   "Fail to get secrets",
		secret: &corev1.Secret{},
		ci: &v1alpha1.ClusterIngress{
			Spec: v1alpha1.IngressSpec{
				TLS: []v1alpha1.IngressTLS{{
					Hosts:           []string{"example.com"},
					SecretName:      "no-exist-secret",
					SecretNamespace: "no-exist-namespace",
				}},
			},
		},
		wantErr: true,
	}}
	for _, c := range cases {
		createSecret(c.secret)
		t.Run(c.name, func(t *testing.T) {
			secrets, err := GetSecrets(c.ci, secretClient.Lister())
			if (err != nil) != c.wantErr {
				t.Fatalf("Test: %s; GetSecrets error = %v, WantErr %v", c.name, err, c.wantErr)
			}
			if diff := cmp.Diff(c.expected, secrets); diff != "" {
				t.Errorf("Unexpected secrets (-want, +got): %v", diff)
			}
		})
	}
}

func TestMakeSecrets(t *testing.T) {
	ctx := TestContextWithLogger(t)
	ctx = config.ToContext(ctx, &config.Config{
		Istio: &config.Istio{
			IngressGateways: []config.Gateway{{
				GatewayName: "test-gateway",
				// The namespace of Istio gateway service is istio-system.
				ServiceURL: "istio-ingressgateway.istio-system.svc.cluster.local",
			}},
		},
	})

	cases := []struct {
		name         string
		originSecret *corev1.Secret
		expected     []*corev1.Secret
	}{{
		name: "target secret namespace (istio-system) is the same as the origin secret namespace (istio-system).",
		originSecret: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "istio-system",
				UID:       "1234",
			},
			Data: map[string][]byte{
				"test-data": []byte("abcd"),
			}},
		expected: []*corev1.Secret{},
	}, {
		name: "target secret namespace (istio-system) is different from the origin secret namespace (knative-serving).",
		originSecret: &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "knative-serving",
				UID:       "1234",
			},
			Data: map[string][]byte{
				"test-data": []byte("abcd"),
			}},
		expected: []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				// Name is generated by TargetSecret function.
				Name: "ingress-1234",
				// Expected secret should be in istio-system which is
				// the ns of Istio gateway service.
				Namespace: "istio-system",
				Labels: map[string]string{
					networking.OriginSecretNameLabelKey:      "test-secret",
					networking.OriginSecretNamespaceLabelKey: "knative-serving",
				},
				OwnerReferences: []metav1.OwnerReference{*kmeta.NewControllerRef(&ci)},
			},
			Data: map[string][]byte{
				"test-data": []byte("abcd"),
			},
		}},
	}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			originSecrets := map[string]*corev1.Secret{
				fmt.Sprintf("%s/%s", c.originSecret.Namespace, c.originSecret.Name): c.originSecret,
			}
			secrets := MakeSecrets(ctx, originSecrets, &ci)
			if diff := cmp.Diff(c.expected, secrets); diff != "" {
				t.Errorf("Unexpected secrets (-want, +got): %v", diff)
			}
		})
	}
}