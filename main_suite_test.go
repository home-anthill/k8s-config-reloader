package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestK8sConfigReloader(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "K8s Config Reloader Suite")
}
