package main

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	costv1alpha1 "github.com/youssef-ar/namespace-cost-governor/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

func buildClient() (client.Client, error) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = costv1alpha1.AddToScheme(scheme)

	config, err := loadConfig()
	if err != nil {
		return nil, err
	}

	return client.New(config, client.Options{Scheme: scheme})
}

func loadConfig() (*rest.Config, error) {
	// try in-cluster first (when running inside a pod)
	if config, err := rest.InClusterConfig(); err == nil {
		return config, nil
	}

	// Fall back to the same loading rules as kubectl. This respects KUBECONFIG,
	// the current context, and the default ~/.kube/config location.
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
}
