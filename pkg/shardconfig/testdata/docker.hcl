root {
  oci = "docker:dind"
}

service "docker" {
  command = ["dockerd"]
}

advertise {
  path "/var/run/dokcer.sock" {
    host = true
  }
}
