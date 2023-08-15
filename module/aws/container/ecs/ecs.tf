locals {
  ecr_services = {
    private = "ecr"
    public  = "ecr-public"
  }
  fargate_os = {
    linux = "LINUX"
  }
  fargate_architecture = {
    x86_64 = "X86_64"
  }

  ecr_repository_account_id = coalesce(try(var.task_definition.docker.registry.ecr.account_id, null), local.account_id)
  ecr_repository_region_name = try(
    (var.task_definition.docker.registry.ecr.privacy == "private" ? coalesce(var.task_definition.docker.registry.ecr.region_name, local.region_name) : "us-east-1"),
    null
  )

  docker_registry_name = try(
    var.task_definition.docker.registry.ecr.privacy == "private" ? "${local.ecr_repository_account_id}.dkr.ecr.${local.ecr_repository_region_name}.${local.dns_suffix}" : "public.ecr.aws/${var.task_definition.docker.registry.ecr.public_alias}",
    var.task_definition.docker.registry.name,
    null
  )
}

locals {
  log_steam_name = "test"
  container_name = "unique"
}

module "ecs" {
  source  = "terraform-aws-modules/ecs/aws"
  version = "5.2.0"

  cluster_name = var.name

  # cluster logging
  # create_cloudwatch_log_group            = true
  # cloudwatch_log_group_retention_in_days = var.log.retention_days
  # cloudwatch_log_group_tags              = var.tags
  # cluster_configuration = {
  #   execute_command_configuration = {
  #     logging = "OVERRIDE"
  #     log_configuration = {
  #       cloud_watch_encryption_enabled = true
  #       # cloud_watch_log_group_name     = local.log_steam_name
  #     }
  #   }
  # }

  # capacity providers
  default_capacity_provider_use_fargate = var.service.deployment_type == "fargate" ? true : false
  fargate_capacity_providers = {
    for key, cp in var.fargate.capacity_provider :
    cp.key => {
      default_capacity_provider_strategy = {
        weight = cp.weight
        base   = cp.base
      }
    }
    if var.service.deployment_type == "fargate"
  }
  autoscaling_capacity_providers = {
    for key, value in var.ec2 :
    key => {
      name                   = "${var.name}-${key}"
      auto_scaling_group_arn = module.asg[key].autoscaling_group_arn
      managed_scaling = {
        // https://docs.aws.amazon.com/AmazonECS/latest/developerguide/service-quotas.html
        maximum_scaling_step_size = value.capacity_provider.maximum_scaling_step_size == null ? max(min(ceil((var.service.task_max_count - var.service.task_min_count) / 3), 10), 1) : value.capacity_provider.maximum_scaling_step_size
        minimum_scaling_step_size = value.capacity_provider.minimum_scaling_step_size == null ? max(min(floor((var.service.task_max_count - var.service.task_min_count) / 10), 10), 1) : value.capacity_provider.minimum_scaling_step_size
        target_capacity           = value.capacity_provider.target_capacity_cpu_percent # utilization for the capacity provider
        status                    = "ENABLED"
        instance_warmup_period    = 300
        default_capacity_provider_strategy = {
          base   = value.capacity_provider.base
          weight = value.capacity_provider.weight
        }
      }
      managed_termination_protection = "DISABLED"
    }
    if var.service.deployment_type == "ec2"
  }

  services = {
    unique = {
      #------------
      # Service
      #------------
      force_new_deployment               = true
      launch_type                        = var.service.deployment_type == "fargate" ? "FARGATE" : "EC2"
      enable_autoscaling                 = true
      autoscaling_min_capacity           = var.service.task_min_count
      desired_count                      = var.service.task_desired_count
      autoscaling_max_capacity           = var.service.task_max_count
      deployment_maximum_percent         = var.service.deployment_maximum_percent         // max % tasks running required
      deployment_minimum_healthy_percent = var.service.deployment_minimum_healthy_percent // min % tasks running required
      deployment_circuit_breaker         = var.service.deployment_circuit_breaker

      # network awsvpc for fargate
      subnets          = var.service.deployment_type == "fargate" ? local.subnets : null
      assign_public_ip = var.service.deployment_type == "fargate" ? true : null // if private subnets, use NAT

      load_balancer = {
        service = {
          target_group_arn = element(module.elb.target_group_arns, 0) // one LB per target group
          container_name   = local.container_name
          container_port   = element([for traffic in local.traffics : traffic.target.port if traffic.base == true || length(local.traffics) == 1], 0)
        }
      }

      # security group
      subnet_ids = local.subnets
      security_group_rules = merge(
        {
          for target in distinct([for traffic in local.traffics : {
            port     = traffic.target.port
            protocol = traffic.target.protocol
            }]) : join("-", ["elb", "ingress", target.protocol, target.port]) => {
            type                     = "ingress"
            from_port                = target.port
            to_port                  = target.port
            protocol                 = local.aws_security_group_rule_protocols[target.protocol]
            description              = "Service ${target.protocol} port ${target.port}"
            source_security_group_id = module.elb_sg.security_group_id
          }
        },
        {
          egress_all = {
            type        = "egress"
            from_port   = 0
            to_port     = 0
            protocol    = "-1"
            cidr_blocks = ["0.0.0.0/0"]
            description = "Allow all traffic"
          }
      })

      #---------------------
      # Task definition
      #---------------------
      create_task_exec_iam_role = true
      task_exec_iam_role_tags   = var.tags
      task_exec_iam_statements = merge(
        {
          custom = {
            actions = [
              # // AmazonECSTaskExecutionRolePolicy for fargate 
              # // AmazonEC2ContainerServiceforEC2Role for ec2
              "ec2:DescribeTags",
              "ecs:CreateCluster",
              "ecs:DeregisterContainerInstance",
              "ecs:DiscoverPollEndpoint",
              "ecs:Poll",
              "ecs:RegisterContainerInstance",
              "ecs:StartTelemetrySession",
              "ecs:UpdateContainerInstancesState",
              "ecs:Submit*",
              "ecs:StartTask",
            ]
            effect    = "Allow"
            resources = ["*"],
          },
          # log-group = {
          #   actions = [
          #     "logs:CreateLogStream",
          #     "logs:PutLogEvents",
          #   ]
          #   effect    = "Allow"
          #   resources = ["arn:${local.partition}:logs:${local.region_name}:${local.account_id}:log-group:${local.log_steam_name}"],
          # },
        },
        try({
          bucket-env = {
            actions   = ["s3:GetBucketLocation", "s3:ListBucket"]
            effect    = "Allow"
            resources = ["arn:${local.partition}:s3:::${var.task_definition.env_file.bucket_name}"],
          },
          bucket-env-files = {
            actions   = ["s3:GetObject"]
            effect    = "Allow"
            resources = ["arn:${local.partition}:s3:::${var.task_definition.env_file.bucket_name}/*"],
          },
        }, {}),
        try(var.task_definition.docker.registry.ecr != null, false) ? {
          ecr = {
            actions = [
              "ecr:GetAuthorizationToken",
              "ecr:BatchCheckLayerAvailability",
              "ecr:GetDownloadUrlForLayer",
              "ecr:BatchGetImage",
              "ecr-public:GetAuthorizationToken",
              "ecr-public:BatchCheckLayerAvailability",
            ]
            effect    = "Allow"
            resources = ["arn:${local.partition}:${local.ecr_services[var.task_definition.docker.registry.ecr.privacy]}:${local.ecr_repository_region_name}:${local.ecr_repository_account_id}:repository/${var.task_definition.docker.repository.name}"]
          },
        } : {}
      )

      create_tasks_iam_role = true
      task_iam_role_tags    = var.tags
      tasks_iam_role_statements = {
        custom = {
          actions = [
            "ec2:Describe*",
          ]
          effect    = "Allow"
          resources = ["*"],
        },
        # log-stream = {
        #   actions = [
        #     "logs:PutLogEvents",
        #   ]
        #   effect    = "Allow"
        #   resources = ["arn:${local.partition}:logs:${local.region_name}:${local.account_id}:log-group:${local.log_steam_name}:log-stream:*"],
        # },
      }

      # Task definition
      memory                   = var.task_definition.memory
      cpu                      = var.task_definition.cpu
      family                   = var.name
      requires_compatibilities = var.service.deployment_type == "fargate" ? ["FARGATE"] : ["EC2"]
      // https://docs.aws.amazon.com/AmazonECS/latest/bestpracticesguide/networking-networkmode.html
      network_mode = var.service.deployment_type == "fargate" ? "awsvpc" : "bridge" // "host" for single instance

      # Task definition container(s)
      # https://github.com/terraform-aws-modules/terraform-aws-ecs/blob/master/modules/container-definition/variables.tf
      container_definitions = {
        "${local.container_name}" = {

          # enable_cloudwatch_logging              = true
          # create_cloudwatch_log_group            = true
          # cloudwatch_log_group_retention_in_days = 30
          # cloudwatch_log_group_kms_key_id        = null

          # name = var.name
          environment_files = try([{
            "value" = "arn:${local.partition}:s3:::${var.task_definition.env_file.bucket_name}/${var.task_definition.env_file.file_name}",
            "type"  = "s3"
            }
          ], [])
          environment = var.task_definition.environment,

          # https://docs.aws.amazon.com/AmazonECS/latest/APIReference/API_PortMapping.html
          port_mappings = [for target in distinct([for traffic in local.traffics : {
            port     = traffic.target.port
            protocol = traffic.target.protocol
            }]) : {
            containerPort = target.port
            hostPort      = var.service.deployment_type == "fargate" ? target.port : 0 // "host" network can use target port 
            name          = join("-", ["container", target.protocol, target.port])
            protocol      = target.protocol
            }
          ]
          memory             = var.task_definition.memory
          memory_reservation = var.task_definition.memory_reservation
          cpu                = var.task_definition.cpu
          log_configuration  = null # other driver than json-file

          resource_requirements = concat(
            var.task_definition.resource_requirements,
            var.service.deployment_type == "ec2" && alltrue([for key, value in var.ec2 : value.architecture == "gpu"]) ? [{
              "type" : "GPU",
              "value" : "${var.task_definition.gpu}"
            }] : []
          )


          command                  = var.task_definition.command
          entrypoint               = var.task_definition.entrypoint
          health_check             = var.task_definition.health_check
          readonly_root_filesystem = var.task_definition.readonly_root_filesystem
          user                     = var.task_definition.user
          volumes_from             = var.task_definition.volumes_from
          working_directory        = var.task_definition.working_directory
          mount_points             = var.task_definition.mount_points

          // fargate AMI
          runtime_platform = var.service.deployment_type == "fargate" ? {
            "operatingSystemFamily" = local.fargate_os[var.fargate.os],
            "cpuArchitecture"       = local.fargate_architecture[var.fargate.architecture],
          } : null

          image = join("/", compact([
            local.docker_registry_name,
            join(":", compact([var.task_definition.docker.repository.name, try(var.task_definition.docker.image.tag, "")]))
          ]))

          essential = true
        }
      }
    }
  }

  tags = var.tags
}
