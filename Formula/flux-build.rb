# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class FluxBuild < Formula
  desc "Build kustomize overlays with flux2 HelmRelease support"
  homepage "https://github.com/DoodleScheduling/flux-build"
  version "0.2.1"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.2.1/flux-build_0.2.1_darwin_amd64.tar.gz"
      sha256 "1d2002f4cdf8795864feab20b599d40571cac29f8b0dbc4e487302286a861541"

      def install
        bin.install "flux-build"
      end
    end
    if Hardware::CPU.arm?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.2.1/flux-build_0.2.1_darwin_arm64.tar.gz"
      sha256 "c55781399ec775f92cd416a1d89ef33d40c9826b872d812c23ecb5de0bfce28d"

      def install
        bin.install "flux-build"
      end
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.2.1/flux-build_0.2.1_linux_arm64.tar.gz"
      sha256 "103b51e2d8c91912fdc0f43b8bd5f2945bdf2a4b943dd96d5e417303bdbe17ef"

      def install
        bin.install "flux-build"
      end
    end
    if Hardware::CPU.intel?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v0.2.1/flux-build_0.2.1_linux_amd64.tar.gz"
      sha256 "af4d0335539853ce94eae42021e19c1be25032b9c109f93a707c6044d3922de5"

      def install
        bin.install "flux-build"
      end
    end
  end

  test do
    system "#{bin}/flux-build -h"
  end
end
