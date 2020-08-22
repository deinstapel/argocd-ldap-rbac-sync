package main

import (
	"github.com/ghodss/yaml"
	"io/ioutil"
	"os"

	"github.com/ericchiang/k8s"
)

func makeKubeconfigClient(path string) (*k8s.Client, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	config := new(k8s.Config)
	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, err
	}
	client, err := k8s.NewClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func makeClient() (*k8s.Client, error) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return makeKubeconfigClient(kubeconfig)
	}
	return k8s.NewInClusterClient()
}
