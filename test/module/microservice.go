package module

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KookaS/infrastructure-modules/test/util"
	terratest_http_helper "github.com/gruntwork-io/terratest/modules/http-helper"
	terratest_logger "github.com/gruntwork-io/terratest/modules/logger"
	"github.com/gruntwork-io/terratest/modules/terraform"
	terratest_structure "github.com/gruntwork-io/terratest/modules/test-structure"
)

var (
	AccountName   = util.GetEnvVariable("AWS_PROFILE")
	AccountId     = util.GetEnvVariable("AWS_ACCOUNT_ID")
	AccountRegion = util.GetEnvVariable("AWS_REGION")
	DomainName    = util.GetEnvVariable("DOMAIN_NAME")
)

const (
	// https://docs.aws.amazon.com/AmazonECS/latest/developerguide/memory-management.html#ecs-reserved-memory
	ECSReservedMemory = 100
)

type EC2Instance struct {
	Name          string
	Cpu           int
	Memory        int
	MemoryAllowed int
}

var (
	// for amazon 2023 at least
	T3Small = EC2Instance{
		Name:          "t3.small",
		Cpu:           2048,
		Memory:        2048,
		MemoryAllowed: 1801, // TODO: double check under infra of cluster + ECSReservedMemory
	}
	T3Medium = EC2Instance{
		Name:          "t3.medium",
		Cpu:           2048,
		Memory:        4096,
		MemoryAllowed: 3828,
	}
)

type GithubProjectInformation struct {
	Organization string
	Repository   string
	Branch       string
	// WorkflowFilename string
	// WorkflowName     string
	HealthCheckPath string
	ImageTag        string
}

type EndpointTest struct {
	Path                string
	ExpectedStatus      int
	ExpectedBody        *string
	MaxRetries          int
	SleepBetweenRetries time.Duration
}

func SetupOptionsMicroservice(t *testing.T, projectName, serviceName string) (*terraform.Options, string) {
	rand.Seed(time.Now().UnixNano())

	// global variables
	id := RandomID(8)
	environment_name := fmt.Sprintf("%s-%s", os.Getenv("ENVIRONMENT_NAME"), id)
	commonName := strings.ToLower(fmt.Sprintf("%s-%s-%s", projectName, serviceName, environment_name))
	commonTags := map[string]string{
		"Account":     AccountName,
		"Region":      AccountRegion,
		"Project":     projectName,
		"Service":     serviceName,
		"Environment": environment_name,
	}

	bucketEnvName := fmt.Sprintf("%s-%s", commonName, "env")

	options := &terraform.Options{
		Vars: map[string]any{
			"common_name": commonName,
			"common_tags": commonTags,

			"microservice": map[string]any{
				"ecs": map[string]any{
					"log": map[string]any{
						"retention_days": 1,
						"prefix":         "aws/ecs",
					},
					"task_definition": map[string]any{
						"env_bucket_name": bucketEnvName,
					},
				},
				"bucket_env": map[string]any{
					"name":          bucketEnvName,
					"force_destroy": true,
					"versioning":    false,
				},
			},
		},
	}
	return options, commonName
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandomID(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func TestRestEndpoints(t *testing.T, endpoints []EndpointTest) {
	sleep := time.Second * 30
	terratest_logger.Log(t, fmt.Sprintf("Sleeping before testing endpoints %s...", sleep))
	time.Sleep(sleep)

	tlsConfig := tls.Config{}
	for _, endpoint := range endpoints {
		options := terratest_http_helper.HttpGetOptions{Url: endpoint.Path, TlsConfig: &tlsConfig, Timeout: 10}
		expectedBody := ""
		if endpoint.ExpectedBody != nil {
			expectedBody = *endpoint.ExpectedBody
		}
		for i := 0; i <= endpoint.MaxRetries; i++ {
			gotStatus, gotBody := terratest_http_helper.HttpGetWithOptions(t, options)
			terratest_logger.Log(t, fmt.Sprintf(`
			got status:: %d
			expected status:: %d
			`, gotStatus, endpoint.ExpectedStatus))
			if endpoint.ExpectedBody != nil {
				terratest_logger.Log(t, fmt.Sprintf(`
			got body:: %s
			expected body:: %s
			`, gotBody, expectedBody))
			}
			if gotStatus == endpoint.ExpectedStatus && (endpoint.ExpectedBody == nil || (endpoint.ExpectedBody != nil && gotBody == expectedBody)) {
				terratest_logger.Log(t, `'HTTP GET to URL %s' successful`, endpoint.Path)
				return
			}
			if i == endpoint.MaxRetries {
				t.Fatalf(`'HTTP GET to URL %s' unsuccessful after %d retries`, endpoint.Path, endpoint.MaxRetries)
			}
			terratest_logger.Log(t, fmt.Sprintf("Sleeping %s...", endpoint.SleepBetweenRetries))
			time.Sleep(endpoint.SleepBetweenRetries)
		}
	}
}

func RunTestMicroservice(t *testing.T, options *terraform.Options, commonName string, microservicePath string, githubProject GithubProjectInformation, endpoints []EndpointTest) {
	options = terraform.WithDefaultRetryableErrors(t, options)

	// defer func() {
	// 	if r := recover(); r != nil {
	// 		// destroy all resources if panic
	// 		terraform.Destroy(t, options)
	// 	}
	// 	terratest_structure.RunTestStage(t, "cleanup", func() {
	// 		terraform.Destroy(t, options)
	// 	})
	// }()

	terratest_structure.RunTestStage(t, "deploy", func() {
		terraform.InitAndApply(t, options)
	})

	terratest_structure.RunTestStage(t, "validate_ecs", func() {
		serviceCount := int64(1)
		TestEcs(t, AccountRegion, commonName, commonName, serviceCount)
	})

	// test Load Balancer HTTP
	elb := microserviceExtractElb(t, microservicePath)
	if elb != nil {
		elbDnsUrl := elb.(map[string]any)["lb_dns_name"].(string)
		elbDnsUrl = "http://" + elbDnsUrl
		fmt.Printf("\n\nLoad Balancer DNS = %s\n\n", elbDnsUrl)

		// add dns to endpoints
		endpointsLoadBalancer := []EndpointTest{}
		for _, endpoint := range endpoints {
			endpointsLoadBalancer = append(endpointsLoadBalancer, EndpointTest{
				Path:                elbDnsUrl + endpoint.Path,
				ExpectedStatus:      endpoint.ExpectedStatus,
				ExpectedBody:        endpoint.ExpectedBody,
				MaxRetries:          endpoint.MaxRetries,
				SleepBetweenRetries: endpoint.SleepBetweenRetries,
			})
		}

		terratest_structure.RunTestStage(t, "validate_rest_endpoints_load_balancer", func() {
			TestRestEndpoints(t, endpointsLoadBalancer)
		})
	}

	// TODO: add HTTPS if no timeout from ACM

	// test Route53
	route53 := microserviceExtractRoute53(t, microservicePath)
	if route53 != nil {
		zoneName := elb.(map[string]any)["zone"].(map[string]any)["name"].(string)
		recordSubdomainName := elb.(map[string]any)["record"].(map[string]any)["subdomain_name"].(string)
		route53DnsUrl := recordSubdomainName + "." + zoneName
		fmt.Printf("\n\nRoute53 DNS = %s\n\n", route53DnsUrl)

		// add dns to endpoints
		endpointsRoute53 := []EndpointTest{}
		for _, endpoint := range endpoints {
			endpointsRoute53 = append(endpointsRoute53, EndpointTest{
				Path:                route53DnsUrl + endpoint.Path,
				ExpectedStatus:      endpoint.ExpectedStatus,
				ExpectedBody:        endpoint.ExpectedBody,
				MaxRetries:          endpoint.MaxRetries,
				SleepBetweenRetries: endpoint.SleepBetweenRetries,
			})
		}

		terratest_structure.RunTestStage(t, "validate_rest_endpoints_route53", func() {
			TestRestEndpoints(t, endpointsRoute53)
		})
	}
}

func microserviceExtractElb(t *testing.T, microservicePath string) any {
	// dnsUrl := terraform.Output(t, options, "alb_dns_name")
	jsonFile, err := os.Open(fmt.Sprintf("%s/terraform.tfstate", microservicePath))
	if err != nil {
		t.Fatal(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	var result map[string]any
	json.Unmarshal([]byte(byteValue), &result)
	return result["outputs"].(map[string]any)["microservice"].(map[string]any)["value"].(map[string]any)["ecs"].(map[string]any)["elb"]
}

func microserviceExtractRoute53(t *testing.T, microservicePath string) any {
	jsonFile, err := os.Open(fmt.Sprintf("%s/terraform.tfstate", microservicePath))
	if err != nil {
		t.Fatal(err)
	}
	defer jsonFile.Close()
	byteValue, _ := ioutil.ReadAll(jsonFile)
	var result map[string]any
	json.Unmarshal([]byte(byteValue), &result)
	return result["outputs"].(map[string]any)["microservice"].(map[string]any)["value"].(map[string]any)["route53"]
}