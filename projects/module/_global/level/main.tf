data "aws_region" "current" {}
data "aws_caller_identity" "current" {}

locals {
  region_name = data.aws_region.current.name
  account_id  = data.aws_caller_identity.current.account_id
}

module "group_project_statements" {
  source = "../../aws/iam/statements/project"

  for_each = { for key, value in var.aws.groups : key => value if length(value.project_names) > 0 }

  root_path     = "../../../.."
  project_names = each.value.project_names
}

module "aws_level" {
  source = "../../../../module/aws/iam/level"

  levels = var.aws.levels
  groups = { for key, value in var.aws.groups : key => {
    force_destroy = value.force_destroy
    pw_length     = value.pw_length
    users         = value.users
    statements    = concat(value.statements, try(module.group_project_statements[key].statements, []))
    }
  }
  statements                = var.aws.statements
  external_assume_role_arns = var.aws.external_assume_role_arns

  store_secrets = var.aws.store_secrets

  tags = var.aws.tags
}

resource "github_actions_variable" "example_variable" {
  for_each = { for product in setproduct(var.github.repositories, var.github.variables) : "${product[0].name}-${product[1].key}" => { repository = product[0], variable = product[1] } }

  repository    = join("/", [each.value.repository.name])
  variable_name = each.value.variable.key
  value         = each.value.variable.value
}

module "github_environments" {
  source = "../../../../module/github/environments"

  for_each = merge([
    for group_name, groups in module.aws_level.groups : merge({
      for user_name, value in groups.users : user_name => value.user if value.github_store_environment
    })
  ]...)

  name             = each.value.iam_user_name
  repository_names = [for repository in var.github.repositories : join("/", [repository.owner, repository.name])]
  variables = [
    { key = "AWS_ACCESS_KEY", value = each.value.iam_access_key_id },
    { key = "AWS_ACCOUNT_ID", value = local.account_id },
    { key = "AWS_PROFILE_NAME", value = each.value.iam_user_name },
    { key = "AWS_REGION_NAME", value = local.region_name },
  ]
  secrets = [
    { key = "AWS_SECRET_KEY", value = merge([
      for group_name, groups in module.aws_level.groups_sensitive : merge({
        for user_name, value in groups.users : user_name => value.user
      })
      ]...)[each.key].iam_access_key_secret
    }
  ]
}