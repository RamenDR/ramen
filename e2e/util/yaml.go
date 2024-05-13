// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package util

import (
	"os"

	"gopkg.in/yaml.v2"
)

type yamlConf struct {
	ChannelName      string `yaml:"channelname,omitempty"`
	ChannelNamespace string `yaml:"channelnamespace,omitempty"`
	GitURL           string `yaml:"giturl,omitempty"`
}

const configFile = "config.yaml"

func getConf() (yamlConf, error) {
	cnf := &yamlConf{}

	data, err := os.ReadFile(configFile)
	if err != nil {
		return *cnf, err
	}

	err = yaml.Unmarshal(data, cnf)
	if err != nil {
		return *cnf, err
	}

	return *cnf, nil
}

func GetChannelNameFromYaml() (string, error) {
	cnf, err := getConf()
	if err != nil {
		return "", err
	}

	return cnf.ChannelName, nil
}

func GetChannelNamespaceFromYaml() (string, error) {
	cnf, err := getConf()
	if err != nil {
		return "", err
	}

	return cnf.ChannelNamespace, nil
}

func GetGitURLFromYaml() (string, error) {
	cnf, err := getConf()
	if err != nil {
		return "", err
	}

	return cnf.GitURL, nil
}
