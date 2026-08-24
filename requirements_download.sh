#!/usr/bin/env bash
# install.sh — installs everything needed to run this project.
# Usage: chmod +x install.sh && ./install.sh

set -e

echo "=== Distributed Job Queue — Dependency Installer ==="
echo ""

OS="$(uname -s)"

# ---------- Helper: check if a command exists ----------
have() { command -v "$1" >/dev/null 2>&1; }

# ---------- macOS ----------
install_mac() {
  echo "Detected macOS."

  if ! have brew; then
    echo "Homebrew not found. Installing Homebrew (needed to install everything else)..."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  else
    echo "Homebrew already installed."
  fi

  if ! have docker; then
    echo "Installing Docker Desktop via Homebrew..."
    brew install --cask docker
    echo ""
    echo ">>> Docker Desktop was installed but needs to be opened manually the first time."
    echo ">>> Opening it now — please click through any setup prompts, then re-run this script."
    open -a Docker || true
    exit 0
  else
    echo "Docker already installed."
  fi

  if ! have go; then
    echo "Installing Go via Homebrew..."
    brew install go
  else
    echo "Go already installed."
  fi

  if ! have hey; then
    echo "Installing 'hey' (load testing tool) via Homebrew..."
    brew install hey
  else
    echo "'hey' already installed."
  fi
}

# ---------- Linux (Debian/Ubuntu) ----------
install_linux() {
  echo "Detected Linux."

  if ! have docker; then
    echo "Installing Docker Engine..."
    curl -fsSL https://get.docker.com -o /tmp/get-docker.sh
    sudo sh /tmp/get-docker.sh
    sudo usermod -aG docker "$USER"
    echo ""
    echo ">>> Docker installed. You must log out and back in for permissions to apply,"
    echo ">>> then re-run this script to continue installing Go and 'hey'."
    exit 0
  else
    echo "Docker already installed."
  fi

  if ! have go; then
    echo "Installing Go..."
    GO_VERSION="1.23.0"
    ARCH="$(uname -m)"
    if [ "$ARCH" = "x86_64" ]; then GOARCH="amd64"; else GOARCH="arm64"; fi
    curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${GOARCH}.tar.gz" -o /tmp/go.tar.gz
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf /tmp/go.tar.gz
    if ! grep -q '/usr/local/go/bin' ~/.bashrc; then
      echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
    fi
    export PATH=$PATH:/usr/local/go/bin
    echo ">>> Go installed. Run 'source ~/.bashrc' or restart your terminal to use it."
  else
    echo "Go already installed."
  fi

  if ! have hey; then
    if have go; then
      echo "Installing 'hey' (load testing tool)..."
      go install github.com/rakyll/hey@latest
    else
      echo "Skipping 'hey' install — Go must be installed and on PATH first (restart terminal, re-run script)."
    fi
  else
    echo "'hey' already installed."
  fi
}

# ---------- Windows (Git Bash / WSL) ----------
install_windows() {
  echo "Detected Windows (Git Bash/WSL)."

  if ! have docker; then
    if have winget; then
      echo "Installing Docker Desktop via winget..."
      winget install -e --id Docker.DockerDesktop
      echo ""
      echo ">>> Docker Desktop installed. Please open it manually, complete setup,"
      echo ">>> then re-run this script."
      exit 0
    else
      echo ">>> 'winget' not found. Opening the Docker Desktop download page —"
      echo ">>> please install it manually, then re-run this script."
      start "" "https://www.docker.com/products/docker-desktop"
      exit 0
    fi
  else
    echo "Docker already installed."
  fi

  if ! have go; then
    if have winget; then
      echo "Installing Go via winget..."
      winget install -e --id GoLang.Go
    else
      echo ">>> Opening the Go download page — please install it manually, then re-run this script."
      start "" "https://go.dev/dl/"
      exit 0
    fi
  else
    echo "Go already installed."
  fi

  if ! have hey; then
    echo "Installing 'hey' (load testing tool)..."
    go install github.com/rakyll/hey@latest
  else
    echo "'hey' already installed."
  fi
}

# ---------- Dispatch by OS ----------
case "$OS" in
  Darwin) install_mac ;;
  Linux) install_linux ;;
  MINGW*|MSYS*|CYGWIN*) install_windows ;;
  *)
    echo "Unrecognized OS: $OS"
    echo "Please install manually: Docker Desktop (docker.com/products/docker-desktop) and Go (go.dev/dl)."
    exit 1
    ;;
esac

echo ""
echo "=== Checking final status ==="
have docker && echo "✅ Docker: $(docker --version)" || echo "❌ Docker not found"
have go && echo "✅ Go: $(go version)" || echo "❌ Go not found"
have hey && echo "✅ hey: installed" || echo "⚠️  hey not found (optional, only needed for load testing)"

echo ""
echo "If Docker shows ✅, run:"
echo "  docker-compose up"
echo "Then open http://localhost:8080"
