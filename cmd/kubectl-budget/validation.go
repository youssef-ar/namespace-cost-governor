package main

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

func validateNamespace(namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", fmt.Errorf("namespace cannot be empty")
	}
	if errs := validation.IsDNS1123Subdomain(namespace); len(errs) > 0 {
		return "", fmt.Errorf("invalid namespace %q: %s", namespace, strings.Join(errs, "; "))
	}
	return namespace, nil
}
