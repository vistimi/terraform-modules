locals {
  microservice_config_vars = yamldecode(file("../../microservice.yml"))
  repository_config_vars   = yamldecode(file("./repository.yml"))
  name_service             = lower(join("-", compact([var.name_prefix, local.repository_config_vars.project_name, local.repository_config_vars.service_name, var.name_suffix])))
  name_project             = lower(join("-", compact([var.name_prefix, local.repository_config_vars.project_name, var.name_suffix])))
}

module "microservice" {
  source = "../../../../../../module/aws/container/microservice"

  name       = local.name_service
  vpc        = var.vpc
  route53    = var.microservice.route53
  ecs        = var.microservice.ecs
  bucket_env = merge(var.microservice.bucket_env, { name = local.microservice_config_vars.bucket_env_name })
  iam        = var.microservice.iam

  tags = var.tags
}

module "dynamodb_table" {
  source = "../../../../../../module/aws/data/dynamodb"

  for_each = {
    for index, dt in var.dynamodb_tables :
    dt.name => dt
  }

  # TODO: handle no sort key
  table_name                   = "${local.name_project}-${each.value.name}"
  primary_key_name             = each.value.primary_key_name
  primary_key_type             = each.value.primary_key_type
  sort_key_name                = each.value.sort_key_name
  sort_key_type                = each.value.sort_key_type
  predictable_workload         = each.value.predictable_workload
  predictable_capacity         = each.value.predictable_capacity
  table_attachement_role_names = [module.microservice.ecs.service.task_iam_role_name]
  iam                          = var.microservice.iam

  tags = var.tags
}

module "bucket_picture" {
  source = "../../../../../../module/aws/data/bucket"

  name                          = "${local.name_project}-${var.bucket_picture.name}"
  force_destroy                 = var.bucket_picture.force_destroy
  versioning                    = var.bucket_picture.versioning
  bucket_attachement_role_names = [module.microservice.ecs.service.task_iam_role_name]
  iam                           = var.microservice.iam

  tags = var.tags
}
