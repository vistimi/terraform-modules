output "s3_bucket_arn" {
  value       = module.terraform_storage.s3_bucket_arn
  description = "The ARN of the S3 bucket"
}

output "locks_arn" {
  value       = aws_dynamodb_table.terraform_locks.arn
  description = "The ARN of the locks"
}