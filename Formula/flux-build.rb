# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class FluxBuild < Formula
  desc "Build kustomize overlays with flux2 HelmRelease support"
  homepage "https://github.com/DoodleScheduling/flux-build"
  version "3.0.10"

  on_macos do
    if Hardware::CPU.intel?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v3.0.10/flux-build_3.0.10_darwin_amd64.tar.gz"
      sha256 "7faa38831e4ecc14b6f43127ba29cd3a48ca25a860b2c1852f327e875509ebc4"

      def install
        bin.install "flux-build"
      end
    end
    if Hardware::CPU.arm?
      url "https://github.com/DoodleScheduling/flux-build/releases/download/v3.0.10/flux-build_3.0.10_darwin_arm64.tar.gz"
      sha256 "33c746d206765aa69a197b8afa24f5f4fddc4cf8fcd9419bde63f2f96a8139bb"

      def install
        bin.install "flux-build"
      end
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      if Hardware::CPU.is_64_bit?
        url "https://github.com/DoodleScheduling/flux-build/releases/download/v3.0.10/flux-build_3.0.10_linux_amd64.tar.gz"
        sha256 "f68c40fff9d818114308e2211d3d3cc7fc2dcbd84b20f1e401e13202301bc753"

        def install
          bin.install "flux-build"
        end
      end
    end
    if Hardware::CPU.arm?
      if Hardware::CPU.is_64_bit?
        url "https://github.com/DoodleScheduling/flux-build/releases/download/v3.0.10/flux-build_3.0.10_linux_arm64.tar.gz"
        sha256 "b82c0bfbbde9929dac1ad5013a8fa2502160110ca97657edf91603cbb95fb05a"

        def install
          bin.install "flux-build"
        end
      end
    end
  end

  test do
    system "#{bin}/flux-build -h"
  end
end
