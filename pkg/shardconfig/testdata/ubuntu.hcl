root {
  url = for_platform({
    amd64 = "amd64-only"
    arm64 = "this-is-arm"
  })
}

