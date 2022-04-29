# Isle

Thanks for your interest in `isle`, Integrated System Linux Environment.

Isle is currently in alpha, but working relatively stably! This page will document features and known issues!

Isle is a bit like [WSL][2] (Windows Subsystem for Linux) for Mac.

## Getting Started

Download the linux zip file from the release URL, unzip it, and put the executable in your $PATH. Then run `linux`, and enjoy Linux!

## Current Release

v0.5.0: [https://github.com/lab47/isle/releases/tag/v0.5.0](https://github.com/lab47/isle/releases/tag/v0.5.0)

## Usage

- Start a linux shell: `linux`
- Stop the background VM: `linux --stop`
- Start the VM in the foreground to allow for easy debugging: `linux --start`
- Get information on what options are available `linux -h`
- The default root password is `root`
- Your users password is the same as your username
- Once sudo is installed in, your user will have sudo access.

## Features

- Automatic port forwarding: If a program listens on 0.0.0.0 within linux, that same port will be forwarded to it from your mac. Makes it easy to do most types of web development. **NOTE**: There is up to a 10 second delay before the port is detected as open.
- File sharing: Within linux, you’ll find that ~/mac contains your home directory on the mac. And also ~/linux/home on mac contains the home directory within linux! These are relatively fast, but they do cross machine boundaries.
- Runs OCI images: All the linux environments are spawned by fetching an OCI image, unpacking it, and running a shell. Ubuntu is the default, but you can use any you like.

## Known Issues

- Upon starting, sometimes users will get a disconnection error. Wait a second and retry a couple times. This is happening because there is a race condition in detecting that the VM is ready to access connections.
- The `Virtual Machine Service` on intel takes up a lot of cpu when idle, currently investigating why.

## Architecture

Isle uses [Virtualization.framework][1], built into macs since 11.0, to spawn a linux VM.
It then uses runc within the VM to provide distro-specific environments.
MacOS 12.0+ is required as it uses the file sharing APIs that were added in 12.0 to provide access to the MacOS filesystem within linux.

## Roadmap

- Ability to run background services by default without weird .profile hacks
- Perhaps a sort of “App Store” setup where you could add things like docker or tailscale to install and then run by default

[1]: https://developer.apple.com/documentation/virtualization
[2]: https://docs.microsoft.com/windows/wsl/about
