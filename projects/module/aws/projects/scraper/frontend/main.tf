locals {
  microservice_config_vars = yamldecode(file("../../microservice.yml"))
  repository_config_vars   = yamldecode(file("./repository.yml"))
  name                     = lower(join("-", compact([var.name_prefix, local.repository_config_vars.project_name, local.repository_config_vars.service_name, var.name_suffix])))
}

module "microservice" {
  source = "../../../../../../module/aws/container/microservice"

  name       = local.name
  tags       = var.tags
  vpc        = var.vpc
  route53    = var.microservice.route53
  ecs        = var.microservice.ecs
  bucket_env = merge(var.microservice.bucket_env, { name = local.microservice_config_vars.bucket_env_name })
  iam        = var.microservice.iam
}