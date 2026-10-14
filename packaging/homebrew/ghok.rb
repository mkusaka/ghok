class Ghok < Formula
  desc "Transparent GitHub CLI wrapper with pre/post hooks and operation normalization"
  homepage "https://github.com/mkusaka/ghok"
  version "__VERSION__"
  license "MIT"

  depends_on "gh"

  on_macos do
    if Hardware::CPU.arm?
      url "__DARWIN_ARM64_URL__"
      sha256 "__DARWIN_ARM64_SHA256__"
    else
      url "__DARWIN_X64_URL__"
      sha256 "__DARWIN_X64_SHA256__"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "__LINUX_ARM64_URL__"
      sha256 "__LINUX_ARM64_SHA256__"
    else
      url "__LINUX_X64_URL__"
      sha256 "__LINUX_X64_SHA256__"
    end
  end

  def install
    bin.install "ghok"
  end

  def caveats
    <<~EOS
      ghok is installed but not active. To route `gh` through ghok, run:
        ghok shim install
      This creates ~/.local/bin/gh -> #{opt_bin}/ghok (shell rc files are not modified);
      make sure ~/.local/bin comes before #{HOMEBREW_PREFIX}/bin in PATH, then check with:
        ghok doctor
      Undo with `ghok shim uninstall`.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ghok version")
  end
end
