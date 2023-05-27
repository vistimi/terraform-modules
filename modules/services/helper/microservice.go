package helper_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	awsSDK "github.com/aws/aws-sdk-go/aws"
	"github.com/likexian/gokit/assert"

	test_aws "github.com/gruntwork-io/terratest/modules/aws"
	test_http_helper "github.com/gruntwork-io/terratest/modules/http-helper"
	test_logger "github.com/gruntwork-io/terratest/modules/logger"
	test_shell "github.com/gruntwork-io/terratest/modules/shell"
	"github.com/gruntwork-io/terratest/modules/terraform"
	test_structure "github.com/gruntwork-io/terratest/modules/test-structure"
)

var (
	AccountName   = os.Getenv("AWS_PROFILE")
	AccountId     = os.Getenv("AWS_ID")
	AccountRegion = os.Getenv("AWS_REGION")
)

const (
	serviceTaskDesiredCountInit  = 0 // before CI/CD pileline
	ServiceTaskDesiredCountFinal = 2 // at least two for
	TaskDefinitionImageTag       = "latest"

	// task definition variables (fargate and ec2 compatible for ease of use)
	Cpu    = 512
	Memory = 1024
	// Memory_reservation := 1024
)

type GithubProjectInformation struct {
	Organization     string
	Repository       string
	Branch           string
	WorkflowFilename string
	WorkflowName     string
	HealthCheckPath  string
}

type EndpointTest struct {
	Url                 string
	ExpectedStatus      int
	ExpectedBody        string
	MaxRetries          int
	SleepBetweenRetries time.Duration
}

func SetupOptionsMicroservice(t *testing.T, projectName, serviceName string) (*terraform.Options, string) {
	rand.Seed(time.Now().UnixNano())

	// setup terraform override variables
	bashCode := `terragrunt init;`
	command := test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	test_shell.RunCommandAndGetOutput(t, command)

	// global variables
	id := RandomID(8)
	environment_name := fmt.Sprintf("%s-%s", os.Getenv("ENVIRONMENT_NAME"), id)
	common_name := strings.ToLower(fmt.Sprintf("%s-%s-%s", projectName, serviceName, environment_name)) // update var
	common_tags := map[string]string{
		"Account":     AccountName,
		"Region":      AccountRegion,
		"Project":     projectName,
		"Service":     serviceName,
		"Environment": environment_name,
	}
	common_tags_json, err := json.Marshal(common_tags)
	if err != nil {
		test_logger.Log(t, err)
	}

	// end variables
	user_data := fmt.Sprintf(`#!/bin/bash
	cat <<'EOF' >> /etc/ecs/ecs.config
	ECS_CLUSTER=%s
	ECS_ENABLE_TASK_IAM_ROLE=true
	ECS_LOGLEVEL=debug
	ECS_AVAILABLE_LOGGING_DRIVERS='["json-file","awslogs"]'
	ECS_ENABLE_AWSLOGS_EXECUTIONROLE_OVERRIDE=true
	ECS_CONTAINER_INSTANCE_TAGS=%s
	ECS_ENABLE_SPOT_INSTANCE_DRAINING=true
	EOF`, common_name, common_tags_json)

	// vpc variables
	vpcId := terraform.Output(t, &terraform.Options{TerraformDir: "../../vpc"}, "vpc_id")
	defaultSecurityGroupId := terraform.Output(t, &terraform.Options{TerraformDir: "../../vpc"}, "default_security_group_id")

	bucketEnvName := fmt.Sprintf("%s-%s", common_name, "env")

	options := &terraform.Options{
		Vars: map[string]any{
			"common_name": common_name,
			"common_tags": common_tags,
			"vpc": map[string]any{
				"id":                 vpcId,
				"security_group_ids": []string{defaultSecurityGroupId},
				"tier":               "Public",
			},

			"log": map[string]any{
				"retention_days": 1,
				"prefix":         "aws/ecs",
			},

			"service_task_desired_count": serviceTaskDesiredCountInit,
			"user_data":                  user_data,

			"task_definition": map[string]any{
				"memory": Memory,
				// "memory_reservation": memory_reservation,
				"cpu":                Cpu,
				"env_bucket_name":    bucketEnvName,
				"registry_image_tag": TaskDefinitionImageTag,
			},

			"bucket_env": map[string]any{
				"name":          bucketEnvName,
				"force_destroy": true,
				"versioning":    false,
			},

			"ecr": map[string]any{
				"image_keep_count": 1,
				"force_destroy":    true,
			},
		},
	}
	return options, common_name
}

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

func RandomID(n int) string {
	b := make([]rune, n)
	for i := range b {
		b[i] = letterRunes[rand.Intn(len(letterRunes))]
	}
	return string(b)
}

func TestMicroservice(t *testing.T, terraformOptions *terraform.Options, githubInformations GithubProjectInformation) {
	// // TODO: plan test for updates
	// // https://github.com/gruntwork-io/terratest/blob/master/test/terraform_aws_example_plan_test.go

	common_name, ok := terraformOptions.Vars["common_name"].(string)
	if !ok {
		test_logger.Log(t, "terraformOptions misses common_name as string")
	}

	test_structure.RunTestStage(t, "validate_ecr", func() {
		testEcr(t, common_name, AccountRegion)
	})

	test_structure.RunTestStage(t, "validate_ecs", func() {
		testEcs(t, common_name, AccountRegion, common_name, strconv.Itoa(Cpu), strconv.Itoa(Memory), ServiceTaskDesiredCountFinal)
	})
}

// Run Github workflow CI/CD to push images on ECR and update ECS
func RunGithubWorkflow(
	t *testing.T,
	githubInformations GithubProjectInformation,
	commandStartWorkflow string,
) {

	bashCode := fmt.Sprintf(`
		%s
		echo "Sleep 10 seconds for spawning action"
		sleep 10s
		echo "Continue to check the status"
		# while workflow status == in_progress, wait
		workflowStatus="preparing"
		while [ "${workflowStatus}" != "completed" ]
		do
			workflowStatus=$(gh run list --repo %s/%s --branch %s --workflow %s --limit 1 | awk '{print $1}')
			echo $workflowStatus
			if [[ $workflowStatus  =~ "could not find any workflows" ]]; then exit 1; fi
			echo "Waiting for status workflow to complete: "${workflowStatus}
			sleep 30s
		done
		echo "Workflow finished: $workflowStatus"
		sleep 10s
		echo "Sleep 10 seconds"
	`,
		commandStartWorkflow,
		githubInformations.Organization,
		githubInformations.Repository,
		githubInformations.Branch,
		githubInformations.WorkflowName,
	)
	command := test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	test_shell.RunCommandAndGetOutput(t, command)
}

func testEcr(t *testing.T, common_name, account_region string) {
	bashCode := fmt.Sprintf(`aws ecr list-images --repository-name %s --region %s --output text --query "imageIds[].[imageTag]" | wc -l`,
		common_name,
		account_region,
	)
	command := test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	output := strings.TrimSpace(test_shell.RunCommandAndGetOutput(t, command))
	ecrImagesAmount, err := strconv.Atoi(output)
	if err != nil {
		test_logger.Log(t, fmt.Sprintf("String to int conversion failed: %s", output))
	}

	assert.Equal(t, 1, ecrImagesAmount, fmt.Sprintf("No image published to repository: %v", ecrImagesAmount))
}

// https://github.com/gruntwork-io/terratest/blob/master/test/terraform_aws_ecs_example_test.go
func testEcs(t *testing.T, family_name, account_region, common_name, cpu, memory string, service_task_desired_count int) {
	// cluster
	cluster := test_aws.GetEcsCluster(t, account_region, common_name)
	services_amount := int64(1)
	assert.Equal(t, services_amount, awsSDK.Int64Value(cluster.ActiveServicesCount))

	// tasks in service
	service := test_aws.GetEcsService(t, account_region, common_name, common_name)
	service_desired_count := int64(service_task_desired_count)
	assert.Equal(t, service_desired_count, awsSDK.Int64Value(service.DesiredCount), "amount of running tasks in service do not match the expected value")

	// latest task definition
	bashCode := fmt.Sprintf(`
	aws ecs list-task-definitions \
		--region %s \
		--family-prefix %s \
		--sort DESC \
		--query 'taskDefinitionArns[0]' \
		--output text
	`,
		account_region,
		family_name,
	)
	command := test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	latestTaskDefinitionArn := strings.TrimSpace(test_shell.RunCommandAndGetOutput(t, command))
	fmt.Printf("\n\nlatestTaskDefinitionArn = %s\n\n", latestTaskDefinitionArn)

	task := test_aws.GetEcsTaskDefinition(t, account_region, latestTaskDefinitionArn)
	assert.Equal(t, cpu, awsSDK.StringValue(task.Cpu))
	assert.Equal(t, memory, awsSDK.StringValue(task.Memory))

	// running tasks
	bashCode = fmt.Sprintf(`
	aws ecs list-tasks \
		--region %s \
		--cluster %s \
		--query 'taskArns[]' \
		--output text
	`,
		account_region,
		common_name,
	)
	command = test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	runningTaskArns := strings.Fields(test_shell.RunCommandAndGetOutput(t, command))
	fmt.Printf("\n\nrunningTaskArns = %v\n\n", runningTaskArns)
	if len(runningTaskArns) == 0 {
		test_logger.Log(t, "No running tasks")
		return
	}

	// tasks definition versions
	runningTasks := ``
	for _, runningTaskArn := range runningTaskArns {
		runningTasks += fmt.Sprintf(`%s `, runningTaskArn)
	}
	bashCode = fmt.Sprintf(`
	aws ecs describe-tasks \
		--region %s \
		--cluster %s \
		--tasks %s \
		--query 'tasks[].[taskDefinitionArn]' \
		--output text
	`,
		account_region,
		common_name,
		runningTasks,
	)
	command = test_shell.Command{
		Command: "bash",
		Args:    []string{"-c", bashCode},
	}
	runningTaskDefinitionArns := strings.Fields(test_shell.RunCommandAndGetOutput(t, command))
	fmt.Printf("\n\nrunningTaskDefinitionArns = %v\n\n", runningTaskDefinitionArns)
	if len(runningTaskDefinitionArns) == 0 {
		test_logger.Log(t, "No running tasks definition")
		return
	}

	for _, runningTaskDefinitionArn := range runningTaskDefinitionArns {
		if latestTaskDefinitionArn != runningTaskDefinitionArn {
			test_logger.Log(t, "The tasks ARN need to match otherwise the latest version is not the one running")
		}
	}
}

// func testCapacityProviders(t *testing.T, account_region, common_name string) {
// 	cluster := test_aws.GetEcsCluster(t, account_region, common_name)
// 	cluster.CapacityProviders
// 	cluster.RegisteredContainerInstancesCount

// 	service := test_aws.GetEcsService(t, account_region, common_name, common_name)
// 	for _, lb := range(service.LoadBalancers) {
// 		lb.
// 	}

// 	service_desired_count := int64(1)
// 	assert.Equal(t, service_desired_count, awsSDK.Int64Value(service.DesiredCount), "amount of running services do not match the expected value")

// 	// latest task definition
// 	bashCode := fmt.Sprintf(`aws ecs list-task-definitions \
// 		--region %s \
// 		--family-prefix %s \
// 		--sort DESC \
// 		--query 'taskDefinitionArns[0]' \
// 		--output text`,
// 		account_region,
// 		family_name,
// 	)
// 	command := test_shell.Command{
// 		Command: "bash",
// 		Args:    []string{"-c", bashCode},
// 	}
// 	latestTaskDefinitionArn := strings.TrimSpace(test_shell.RunCommandAndGetOutput(t, command))
// 	fmt.Printf("\nlatestTaskDefinitionArn = %s\n", latestTaskDefinitionArn)

// }

func TestRestEndpoints(t *testing.T, endpoints []EndpointTest) {
	tlsConfig := tls.Config{}
	for _, endpoint := range endpoints {
		test_http_helper.HttpGetWithRetry(t, endpoint.Url, &tlsConfig, endpoint.ExpectedStatus, endpoint.ExpectedBody, endpoint.MaxRetries, endpoint.SleepBetweenRetries)
	}
}