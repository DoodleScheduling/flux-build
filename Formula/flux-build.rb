# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class FluxBuild < Formula
  desc "Build kustomize overlays with flux2 HelmRelease support"
  homepage "https://github.com/DoodleScheduling/flux-build"
  version "0.1.0"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.1.0/flux-build_0.1.0_darwin_amd64.tar.gz"
      sha256 "c3a4f620f23fafbb5853d86d171357db5073e950368ec83f9d8859fcc8e4db6b"

      def install
        bin.install "flux-build"
      end
    end
    if Hardware::CPU.arm?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.1.0/flux-build_0.1.0_darwin_arm64.tar.gz"
      sha256 "afac16155321c22844770a130c2d2c9a36d265989f908c9fab3eb3f22f8de447"

      def install
        bin.install "flux-build"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.1.0/flux-build_0.1.0_linux_amd64.tar.gz"
      sha256 "d37d5c70567f370a83b0fea477161db7a5527aa0a10d4f452e04fe999283f84e"

      def install
        bin.install "flux-build"
      end
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.1.0/flux-build_0.1.0_linux_arm64.tar.gz"
      sha256 "1c50e76f7812d99f7f4537df796865b058984686ffaa5fc9f317ed9c7304ea7f"

      def install
        bin.install "flux-build"
      end
    end
  end

  test do
    system "#{bin}/flux-build -h"
  end
end
