data "aws_region" "current" {}
data "aws_partition" "current" {}
data "aws_caller_identity" "current" {}

data "aws_vpc" "current" {
  id = var.vpc.id
}
data "aws_subnets" "tier" {
  filter {
    name   = "vpc-id"
    values = [data.aws_vpc.current.id]
  }
  tags = {
    Tier = var.vpc.tier
  }

  lifecycle {
    postcondition {
      condition     = length(self.ids) >= 2
      error_message = "For a Load Balancer: At least two subnets in two different Availability Zones must be specified, tier: ${var.vpc.tier}, subnets: ${jsonencode(self.ids)}"
    }
  }
}

locals {
  account_id  = data.aws_caller_identity.current.account_id
  account_arn = data.aws_caller_identity.current.arn
  dns_suffix  = data.aws_partition.current.dns_suffix // amazonaws.com
  partition   = data.aws_partition.current.partition  // aws
  region_name = data.aws_region.current.name
  subnets     = data.aws_subnets.tier.ids

  traffics = [for traffic in var.traffics : {
    listener = {
      protocol = traffic.listener.protocol
      port = coalesce(
        traffic.listener.port,
        traffic.listener.protocol == "http" ? 80 : null,
        traffic.listener.protocol == "https" ? 443 : null,
      )
      protocol_version = traffic.listener.protocol_version
    }
    target = {
      protocol         = traffic.target.protocol
      port             = traffic.target.port
      protocol_version = traffic.target.protocol_version
      health_check_path = coalesce(
        traffic.target.health_check_path,
        "/",
      )
    }
    base = traffic.base
  }]

  # icmp, icmpv6, tcp, udp, or all use the protocol number
  # https://www.iana.org/assignments/protocol-numbers/protocol-numbers.xhtml
  aws_security_group_rule_protocols = {
    http    = "tcp"
    https   = "tcp"
    tcp     = "tcp"
    udp     = "udp"
    tcp_udp = "tcp"
    ssl     = "tcp"
  }
}
