package microservice_scraper_backend_test

import (
	"fmt"
	"testing"

	"golang.org/x/exp/maps"

	testAwsProjectModule "github.com/dresspeng/infrastructure-modules/projects/test/aws/module"
	testAwsModule "github.com/dresspeng/infrastructure-modules/test/aws/module"
	"github.com/dresspeng/infrastructure-modules/test/util"
	"github.com/gruntwork-io/terratest/modules/terraform"
	terratestStructure "github.com/gruntwork-io/terratest/modules/test-structure"
)

func Test_Unit_Microservice_ScraperBackend_ECS_EC2(t *testing.T) {
	// t.Parallel()
	namePrefix, nameSuffix, tags, traffics, docker, bucketEnv := testAwsProjectModule.SetupMicroservice(t, MicroserviceInformation, Traffics)
	vars := SetupVars(t)
	serviceNameSuffix := "unique"

	fmt.Println(namePrefix, nameSuffix, tags, traffics, docker, bucketEnv, vars)

	options := util.Ptr(terraform.Options{
		TerraformDir: MicroservicePath,
		Vars: map[string]interface{}{
			"name_prefix": namePrefix,
			"name_suffix": nameSuffix,

			"vpc": map[string]any{
				"id":   util.GetEnvVariable("VPC_ID"),
				"tier": "public",
			},

			"microservice": map[string]any{
				"container": map[string]any{
					"group": map[string]any{
						"name": serviceNameSuffix,
						"deployment": map[string]any{
							"min_size":     1,
							"max_size":     1,
							"desired_size": 1,

							"container": map[string]any{
								"name":                     "unique",
								"docker":                   docker,
								"readonly_root_filesystem": true,
							},
						},

						"ec2": map[string]any{
							"key_name":       nil,
							"instance_types": []string{"t3.small"},
							"os":             "linux",
							"os_version":     "2023",

							"capacities": []map[string]any{
								{
									"type":   "ON_DEMAND",
									"base":   nil, // no preferred instance amount
									"weight": 50,  // 50% chance
								},
							},
						},
					},

					"traffics": traffics,
					"ecs":      map[string]any{},
				},
				"iam": map[string]any{
					"scope":        "accounts",
					"requires_mfa": false,
				},
				"bucket_env": bucketEnv,
			},

			"tags": tags,
		},
	})
	maps.Copy(options.Vars, vars)

	defer func() {
		if r := recover(); r != nil {
			// destroy all resources if panic
			terraform.Destroy(t, options)
		}
		terratestStructure.RunTestStage(t, "cleanup", func() {
			terraform.Destroy(t, options)
		})
	}()

	terratestStructure.RunTestStage(t, "deploy", func() {
		terraform.Init(t, options)
		terraform.Plan(t, options)
		terraform.Apply(t, options)
	})
	terratestStructure.RunTestStage(t, "validate", func() {
		// TODO: test that /etc/ecs/ecs.config is not empty, requires key_name coming from terratest maybe
		name := util.Format("-", namePrefix, projectName, serviceName, nameSuffix)
		serviceName := util.Format("-", name, serviceNameSuffix)
		testAwsModule.ValidateMicroservice(t, name, Deployment, serviceName)
		testAwsModule.ValidateRestEndpoints(t, MicroservicePath, Deployment, Traffics, name, "")
	})
}
