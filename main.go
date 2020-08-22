package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"github.com/ericchiang/k8s"
	corev1 "github.com/ericchiang/k8s/apis/core/v1"
	metav1 "github.com/ericchiang/k8s/apis/meta/v1"
	"github.com/go-ldap/ldap/v3"
	"github.com/spf13/viper"
	"net/http"
	"os"
	"strings"
)

type LDAPConfiguration struct {
	host string
	bindUser string
	bindPass string
	userBaseDN string
	userFilter string
	groupBaseDN string
	groupFilter string
}

type ArgoConfiguration struct {
	host string
	username string
	password string
	token *string
}

type ArgoSessionResponse struct {
	Token string `json:"token"`
}

type ArgoSessionRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ArgoProjectsListResponse struct {
	Items []ArgoProject `json: "items"`
}
type ArgoProjectCreateRequest struct {
	Project ArgoProject `json: "project"`
	Upsert bool `json: "upsert"`
}

type ArgoProject struct {
	Metadata ArgoMeta `json:"metadata"`
	Spec struct{
		Description string `json:"description"`
	} `json:"spec"`
}

type ArgoMeta struct{
	Name string `json:"name"`
}

func httpRequest(argoConfiguration *ArgoConfiguration, httpMethod string, url string, body interface{}) ( *http.Response, error) {
	var bearer = "Bearer " + *argoConfiguration.token

	bodyJson, err := json.Marshal(body)
	req, err := http.NewRequest(httpMethod, argoConfiguration.host + url, strings.NewReader(string(bodyJson)))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Authorization", bearer)
	client := &http.Client{}
	resp, err := client.Do(req)

	return resp, err
}

func syncGroups (ldapConfiguration *LDAPConfiguration, ldapConnection *ldap.Conn, argoConfiguration *ArgoConfiguration, kubernetesClient *k8s.Client) error {

	result, err := ldapConnection.Search(ldap.NewSearchRequest(
		ldapConfiguration.groupBaseDN,
		ldap.ScopeWholeSubtree,
		ldap.NeverDerefAliases,
		0,
		0,
		false,
		ldapConfiguration.groupFilter,
		nil,
		nil,
	))

	if err != nil {
		return err;
	}

	resp, err := httpRequest(argoConfiguration, "GET", "/api/v1/projects", nil)
	if err != nil {
		return err
	}

	var projectListResponse ArgoProjectsListResponse
	err = json.NewDecoder(resp.Body).Decode(&projectListResponse)
	if err != nil {
		return err
	}

	var csvStringBuilder strings.Builder
	csvFile := csv.NewWriter(&csvStringBuilder)

	for _, r := range result.Entries {
		var groupCN string
		for _, a := range r.Attributes {
			if a.Name == "cn" {
				groupCN = a.Values[0]
			}
		}

		var existingProject *ArgoProject
		for _, p := range projectListResponse.Items {
			if p.Metadata.Name == groupCN {
				existingProject = &p;
			}
		}

		if existingProject == nil {

			projectToCreate := ArgoProject{
				Metadata: ArgoMeta{ Name: groupCN },
				Spec: struct{ Description string `json:"description"`}{Description: "Created by argocd-ldap-rbac-sync"},
			}

			pC := ArgoProjectCreateRequest{
				Project: projectToCreate,
				Upsert: false,
			}

			_, err := httpRequest(argoConfiguration, "POST", "/api/v1/projects", pC)
			if err != nil {
				return err
			}

		}

		// Generating CSV Rules

		roleName := groupCN + "-role"
		csvFile.Write([]string{"p", "role:" + roleName, "*", "*", groupCN + "/*", "allow"})
		csvFile.Write([]string{"g", groupCN, "role:" + roleName})

	}

	csvFile.Flush()

	rbacConfigMap := &corev1.ConfigMap{
		Metadata: &metav1.ObjectMeta{
			Name: k8s.String("argocd-rbac-cm"),
			Namespace: k8s.String("argocd"),
		},
		Data: map[string]string{
			"policy.default": "role:readonly",
			"policy.csv": csvStringBuilder.String(),
		},
	}

	return kubernetesClient.Update(context.Background(), rbacConfigMap)

}

func syncRBAC() {

}

func initLDAP(ldapConfiguration *LDAPConfiguration) (*ldap.Conn, error) {

	ldapConnection, err := ldap.DialURL(ldapConfiguration.host)
	if err != nil {
		return nil, err
	}

	err = ldapConnection.Bind(ldapConfiguration.bindUser, ldapConfiguration.bindPass)
	if err != nil {
		return nil, err
	}

	return ldapConnection, nil


}

func initArgoConnection (argoConfiguration *ArgoConfiguration) (*ArgoConfiguration, error) {

	requestContent := ArgoSessionRequest{
		Username: argoConfiguration.username,
		Password: argoConfiguration.password,
	}
	requestContentBody, err := json.Marshal(requestContent)

	resp, err := http.Post(argoConfiguration.host + "/api/v1/session", "application/json", strings.NewReader(string(requestContentBody)))
	if err != nil {
		return nil, err
	}

	var sessionResponse ArgoSessionResponse
	err = json.NewDecoder(resp.Body).Decode(&sessionResponse)
	if err != nil {
		return nil, err
	}

	argoConfiguration.token = &sessionResponse.Token

	return argoConfiguration, nil
}

func initFunc() error {

	viper.AutomaticEnv();

	ldapConfiguration := LDAPConfiguration{
		host:        viper.GetString("LDAP_HOST"),
		bindUser:    viper.GetString("LDAP_BIND_USER"),
		bindPass:    viper.GetString("LDAP_BIND_PASS"),
		userBaseDN:  viper.GetString("LDAP_USER_BASE_DN"),
		userFilter:  viper.GetString("LDAP_USER_FILTER"),
		groupBaseDN: viper.GetString("LDAP_GROUP_BASE_DN"),
		groupFilter: viper.GetString("LDAP_GROUP_FILTER"),
	}

	argoConfiguration := ArgoConfiguration{
		host: viper.GetString("ARGO_HOST"),
		username: viper.GetString("ARGO_USER"),
		password: viper.GetString("ARGO_PASS"),
		token: nil,
	}

	ldapConnection, err := initLDAP(&ldapConfiguration);

	if err != nil {
		return err
	}

	argoConfigurationRef, err := initArgoConnection(&argoConfiguration);

	if err != nil {
		return err
	}

	kubernetesClient, err := makeClient()

	argoConfiguration = *argoConfigurationRef

	err = syncGroups(&ldapConfiguration, ldapConnection, &argoConfiguration, kubernetesClient)
	if err != nil {
		return err
	}

	return nil
}

func main() {

	err := initFunc();
	if err != nil {
		fmt.Printf("Error: %v \n", err)
		os.Exit(1)
	}
}
