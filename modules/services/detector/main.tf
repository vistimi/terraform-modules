locals {
  all_cidrs_ipv4 = "0.0.0.0/0"
  # all_cidrs_ipv6 = "::/0"

  bucket_name_pictures = "${var.common_name}-pictures"
  bucket_name_mongodb  = "${var.common_name}-mongodb"
}

data "aws_vpc" "selected" {
  id = var.vpc_id
}

data "aws_subnets" "private" {
  filter {
    name   = "vpc-id"
    values = [var.vpc_id]
  }

  tags = {
    Tier = "Private"
  }
}

data "aws_subnets" "public" {
  filter {
    name   = "vpc-id"
    values = [var.vpc_id]
  }

  tags = {
    Tier = "Public"
  }
}

resource "tls_private_key" "this" {
  algorithm = "RSA"
  rsa_bits  = 4096
}

resource "aws_key_pair" "key_pair" {

  key_name   = var.common_name
  public_key = tls_private_key.this[0].public_key_openssh

  provisioner "local-exec" {
    command = <<-EOT
      echo "${tls_private_key.this[0].private_key_pem}" > ${var.common_name}.pem
    EOT
  }
}

module "ec2_instance_sg_mongodb" {
  source = "terraform-aws-modules/security-group/aws"

  name        = "${var.common_name}-sg-ssh"
  description = "Security group for Mongodb"
  vpc_id      = var.vpc_id

  ingress_cidr_blocks = [local.all_cidrs_ipv4]
  ingress_rules       = ["mongodb-27017-tcp"]
}

module "ec2_instance_mongodb" {
  source = "../../components/ec2-instance"

  subnet_id = data.aws_subnets.private.ids[0]
  vpc_security_group_ids = concat(
    var.vpc_security_group_ids,
    concat(
      [module.ec2_instance_sg_mongodb.security_group_id],
      var.bastion ? [module.ec2_instance_sg_ssh.security_group_id] : []
    )
  )
  common_tags    = var.common_tags
  cluster_name   = "${var.common_name}-mongodb"
  ami_id         = var.ami_id
  key_name       = var.bastion ? var.common_name : null
  instance_type  = var.instance_type
  user_data_path = var.user_data_path
  user_data_args = merge(var.user_data_args, {
    bucket_name_mongodb : local.bucket_name_mongodb,
    bucket_name_pictures : local.bucket_name_pictures,
  })
  aws_access_key              = var.aws_access_key
  aws_secret_key              = var.aws_secret_key
  associate_public_ip_address = false
}