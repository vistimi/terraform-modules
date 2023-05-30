# endpoint
output "vpc_tier_ids" {
  value       = module.microservice.vpc_tier_ids
  description = "IDs of the subnets selected"
}

# output "autoscaling_group_name_on_demand" {
#   value       = module.microservice.autoscaling_group_name_on_demand
#   description = "The name of the Auto Scaling Group"
# }

# output "autoscaling_group_arn_on_demand" {
#   value       = module.microservice.autoscaling_group_arn_on_demand
#   description = "ARN of the Auto Scaling Group"
# }

# output "autoscaling_group_name_spot" {
#   value       = module.microservice.autoscaling_group_name_spot
#   description = "The name of the Auto Scaling Group"
# }

# output "autoscaling_group_arn_spot" {
#   value       = module.microservice.autoscaling_group_arn_spot
#   description = "ARN of the Auto Scaling Group"
# }

output "alb_dns_name" {
  value       = module.microservice.alb_dns_name
  description = "The domain name of the load balancer"
}

output "alb_security_group_id" {
  value       = module.microservice.alb_security_group_id
  description = "The ID of the security group"
}

# output "ecs_task_definition_arn" {
#   value       = module.microservice.ecs_task_definition_arn
#   description = "Full ARN of the Task Definition (including both family and revision)"
# }

# output "ecs_task_definition_revision" {
#   value       = module.microservice.ecs_task_definition_revision
#   description = "Revision of the task in a particular family"
# }

# Dynamodb

output "dynamodb_tables_arn" {
  value = {
    for k, db in module.dynamodb_table : k => db.dynamodb_table_arn
  }
  description = "The ARNs of the dynamodb tables"
}
