"""Core sandbox management logic."""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Callable

import docker
from docker.errors import DockerException

from sb.naming import generate_name

if TYPE_CHECKING:
    from docker import DockerClient
    from docker.models.containers import Container
    from docker.models.images import Image


# Shell config directory (user-editable configs mounted into containers)
SHELL_CONFIG_DIR = Path.home() / ".config" / "sb"

# Default Docker image name
DEFAULT_IMAGE_NAME = "sb-sandbox:latest"

# Built-in sensitive directories that trigger warnings
SENSITIVE_DIRS = frozenset(
    [
        "/",
        str(Path.home()),
        "/etc",
        "/var",
        "/usr",
        "/bin",
        "/sbin",
    ]
)

# Default mounts configuration (host_path, container_path, mode)
DEFAULT_MOUNTS: list[tuple[str, str, str]] = [
    ("~/.claude/", "/home/sandbox/.claude/", "rw"),  # Claude Code needs write for debug logs
    ("~/.claude.json", "/home/sandbox/.claude.json", "rw"),  # Claude Code config
    ("~/.config/claude-code/", "/home/sandbox/.config/claude-code/", "rw"),  # Claude Code settings
    ("~/.codex/", "/home/sandbox/.codex/", "rw"),  # Codex needs write access
    ("~/.gitconfig", "/home/sandbox/.gitconfig", "ro"),
    # Shell configuration (user-editable)
    ("~/.config/sb/zshrc", "/home/sandbox/.zshrc", "ro"),
    ("~/.config/sb/starship.toml", "/home/sandbox/.config/starship.toml", "ro"),
    ("~/.config/sb/nvim/", "/home/sandbox/.config/nvim/", "rw"),  # rw for plugin install
]


@dataclass
class SandboxInfo:
    """Information about a sandbox."""

    name: str
    workspace: str
    created_at: str
    container_id: str | None = None


@dataclass
class SandboxManager:
    """Manages Docker sandbox containers."""

    image_name: str = DEFAULT_IMAGE_NAME
    extra_mounts: list[str] = field(default_factory=list)
    env_passthrough: list[str] = field(default_factory=list)
    custom_sensitive_dirs: list[str] = field(default_factory=list)

    _client: DockerClient | None = field(default=None, init=False, repr=False)

    def __post_init__(self) -> None:
        """Initialize the Docker client."""
        self._init_client()

    def _init_client(self) -> None:
        """Initialize the Docker client."""
        try:
            self._client = docker.from_env()
            # Test connection
            self._client.ping()
        except DockerException as e:
            raise RuntimeError(
                "Failed to connect to Docker. Is Docker running?"
            ) from e

    @property
    def client(self) -> DockerClient:
        """Get the Docker client, raising if not initialized."""
        if self._client is None:
            raise RuntimeError("Docker client not initialized")
        return self._client

    def _list_sb_containers(self, all_containers: bool = True) -> list[Container]:
        """List all sb-managed Docker containers."""
        return self.client.containers.list(
            all=all_containers,
            filters={"label": "sb.managed=true"},
        )

    def _container_to_sandbox_info(self, container: Container) -> SandboxInfo:
        """Convert a Docker container to SandboxInfo."""
        labels = container.labels
        return SandboxInfo(
            name=labels.get("sb.name", container.name),
            workspace=labels.get("sb.workspace", ""),
            created_at=container.attrs.get("Created", ""),
            container_id=container.id,
        )

    def _get_dockerfile_path(self) -> Path:
        """Get the path to the Dockerfile."""
        return Path(__file__).parent / "docker" / "Dockerfile"

    def _get_image(self) -> Image:
        """Build the sandbox image if needed, return image object.

        Returns:
            The Docker image object.

        Raises:
            RuntimeError: If the image cannot be built or found.
        """
        # First, check if image already exists
        try:
            image = self.client.images.get(self.image_name)
            return image
        except docker.errors.ImageNotFound:
            pass

        # Build the image
        dockerfile_path = self._get_dockerfile_path()
        if not dockerfile_path.exists():
            raise RuntimeError(f"Dockerfile not found at {dockerfile_path}")

        # Build from the docker directory context
        docker_dir = dockerfile_path.parent
        image, logs = self.client.images.build(
            path=str(docker_dir),
            tag=self.image_name,
            rm=True,
        )
        return image

    def _build_mounts(
        self,
        workspace: str,
        extra_cli_mounts: list[str] | None = None,
    ) -> list[docker.types.Mount]:
        """Construct mount list with appropriate ro/rw modes.

        Args:
            workspace: The workspace directory to mount read-write.
            extra_cli_mounts: Additional mounts from CLI (read-only).

        Returns:
            List of Docker Mount objects.
        """
        mounts: list[docker.types.Mount] = []

        # Workspace mount (read-write)
        workspace_path = os.path.abspath(os.path.expanduser(workspace))
        mounts.append(
            docker.types.Mount(
                target="/home/sandbox/workspace",
                source=workspace_path,
                type="bind",
                read_only=False,
            )
        )

        # Default config mounts (read-only)
        for host_path, container_path, mode in DEFAULT_MOUNTS:
            expanded_host = os.path.expanduser(host_path)
            if os.path.exists(expanded_host):
                mounts.append(
                    docker.types.Mount(
                        target=container_path,
                        source=os.path.abspath(expanded_host),
                        type="bind",
                        read_only=(mode == "ro"),
                    )
                )

        # Extra mounts from config (read-only)
        for host_path in self.extra_mounts:
            expanded_host = os.path.expanduser(host_path)
            if os.path.exists(expanded_host):
                # Use the same path inside container, under /home/sandbox
                container_path = host_path.replace("~", "/home/sandbox")
                mounts.append(
                    docker.types.Mount(
                        target=container_path,
                        source=os.path.abspath(expanded_host),
                        type="bind",
                        read_only=True,
                    )
                )

        # Extra mounts from CLI (read-only)
        if extra_cli_mounts:
            for host_path in extra_cli_mounts:
                expanded_host = os.path.expanduser(host_path)
                if os.path.exists(expanded_host):
                    container_path = host_path.replace("~", "/home/sandbox")
                    mounts.append(
                        docker.types.Mount(
                            target=container_path,
                            source=os.path.abspath(expanded_host),
                            type="bind",
                            read_only=True,
                        )
                    )

        return mounts

    def _check_sensitive_dir(self, path: str) -> str | None:
        """Check if path is a sensitive directory that should trigger a warning.

        Args:
            path: The filesystem path to check.

        Returns:
            Warning message if sensitive, None otherwise.
        """
        abs_path = os.path.abspath(os.path.expanduser(path))

        # Combine built-in and custom sensitive directories
        all_sensitive = SENSITIVE_DIRS | set(self.custom_sensitive_dirs)

        for sensitive_dir in all_sensitive:
            expanded_sensitive = os.path.abspath(os.path.expanduser(sensitive_dir))
            if abs_path == expanded_sensitive:
                return (
                    f"Warning: Creating sandbox with access to '{abs_path}' is "
                    f"potentially dangerous.\nThis gives the sandbox write access "
                    f"to this directory.\nContinue? [y/N]:"
                )

        return None

    def _get_uid_gid(self) -> tuple[int, int]:
        """Get the current user's UID and GID."""
        return os.getuid(), os.getgid()

    def _get_default_configs_dir(self) -> Path:
        """Get the path to the default config files shipped with sb."""
        return Path(__file__).parent / "docker" / "configs"

    def _ensure_shell_configs(self) -> None:
        """Ensure shell configuration files exist, copying defaults if needed.

        Creates ~/.config/sb/ directory and populates it with default configs
        for zshrc, starship.toml, and nvim if they don't already exist.
        """
        SHELL_CONFIG_DIR.mkdir(parents=True, exist_ok=True)
        defaults_dir = self._get_default_configs_dir()

        # Copy zshrc if missing
        zshrc_dest = SHELL_CONFIG_DIR / "zshrc"
        if not zshrc_dest.exists():
            zshrc_src = defaults_dir / "zshrc"
            if zshrc_src.exists():
                shutil.copy2(zshrc_src, zshrc_dest)

        # Copy starship.toml if missing
        starship_dest = SHELL_CONFIG_DIR / "starship.toml"
        if not starship_dest.exists():
            starship_src = defaults_dir / "starship.toml"
            if starship_src.exists():
                shutil.copy2(starship_src, starship_dest)

        # Copy nvim config directory if missing
        nvim_dest = SHELL_CONFIG_DIR / "nvim"
        if not nvim_dest.exists():
            nvim_src = defaults_dir / "nvim"
            if nvim_src.exists():
                shutil.copytree(nvim_src, nvim_dest)

    def get_sandbox(self, name: str) -> SandboxInfo | None:
        """Get sandbox info by name."""
        try:
            container = self.client.containers.get(name)
            # Verify it's an sb-managed container
            if container.labels.get("sb.managed") != "true":
                return None
            return self._container_to_sandbox_info(container)
        except docker.errors.NotFound:
            return None

    def get_sandbox_for_path(self, path: str) -> SandboxInfo | None:
        """Get sandbox info for a given workspace path."""
        name = generate_name(path)
        return self.get_sandbox(name)

    def list_sandboxes(self) -> list[SandboxInfo]:
        """List all sb-managed sandboxes."""
        containers = self._list_sb_containers()
        return [self._container_to_sandbox_info(c) for c in containers]

    def get_container_status(self, sandbox: SandboxInfo) -> str:
        """Get the current status of a sandbox's container.

        Returns:
            'running', 'stopped', or 'unknown' (container not found).
        """
        if not sandbox.container_id:
            return "unknown"

        try:
            container = self.client.containers.get(sandbox.container_id)
            return container.status
        except docker.errors.NotFound:
            return "unknown"

    def create(
        self,
        workspace: str | None = None,
        name: str | None = None,
        force: bool = False,
        extra_mounts: list[str] | None = None,
        env_vars: list[str] | None = None,
        image: str | None = None,
        confirm_callback: Callable[[str], bool] | None = None,
    ) -> SandboxInfo:
        """Create a new sandbox for the given workspace.

        Args:
            workspace: The workspace directory (defaults to current directory).
            name: Explicit sandbox name (auto-generated if not provided).
            force: If True, recreate sandbox if it exists without prompting.
            extra_mounts: Additional read-only mounts from CLI.
            env_vars: Environment variables to pass to the container.
            image: Override the Docker image to use.
            confirm_callback: Function to call for user confirmation. Should
                accept a message string and return True if confirmed.

        Returns:
            SandboxInfo for the created sandbox.

        Raises:
            RuntimeError: If sandbox creation fails.
            ValueError: If user declines to proceed.
        """
        # Resolve workspace path
        if workspace is None:
            workspace = os.getcwd()
        workspace = os.path.abspath(os.path.expanduser(workspace))

        # Generate or use provided name
        sandbox_name = name or generate_name(workspace)

        # Check if sandbox already exists
        existing = self.get_sandbox(sandbox_name)
        if existing:
            if not force:
                if confirm_callback:
                    msg = (
                        f"Sandbox '{sandbox_name}' already exists.\n"
                        "Do you want to recreate it? [y/N]:"
                    )
                    if not confirm_callback(msg):
                        raise ValueError("Sandbox creation cancelled by user")
                else:
                    raise ValueError(
                        f"Sandbox '{sandbox_name}' already exists. "
                        "Use --force to recreate."
                    )
            # Destroy the existing sandbox
            self._destroy_container(existing)

        # Check for sensitive directory
        warning = self._check_sensitive_dir(workspace)
        if warning and not force:
            if confirm_callback:
                if not confirm_callback(warning):
                    raise ValueError("Sandbox creation cancelled by user")
            else:
                raise ValueError(
                    f"Workspace is a sensitive directory. Use --force to override."
                )

        # Ensure shell configs exist (copy defaults if needed)
        self._ensure_shell_configs()

        # Build or get the image
        image_name = image or self.image_name
        if image:
            # Use custom image, pull if needed
            try:
                self.client.images.get(image_name)
            except docker.errors.ImageNotFound:
                self.client.images.pull(image_name)
        else:
            # Use built-in image, build if needed
            self._get_image()

        # Build mounts
        mounts = self._build_mounts(workspace, extra_mounts)

        # Get UID/GID for permission mapping
        uid, gid = self._get_uid_gid()

        # Build environment variables
        environment = {
            "HOST_UID": str(uid),
            "HOST_GID": str(gid),
        }
        all_env_vars = list(self.env_passthrough) + (env_vars or [])
        for env_var in all_env_vars:
            if "=" in env_var:
                # Format: VAR=value
                key, value = env_var.split("=", 1)
                environment[key] = value
            else:
                # Format: VAR (pass through from host)
                value = os.environ.get(env_var)
                if value:
                    environment[env_var] = value

        # Labels for tracking sandbox metadata
        labels = {
            "sb.managed": "true",
            "sb.name": sandbox_name,
            "sb.workspace": workspace,
        }

        # Create the container
        # Note: Container starts as root, entrypoint.sh creates user and drops privileges
        container = self.client.containers.create(
            image=image_name,
            name=sandbox_name,
            mounts=mounts,
            environment=environment,
            labels=labels,
            working_dir="/home/sandbox/workspace",
            stdin_open=True,
            tty=True,
            detach=True,
        )

        # Return sandbox info from container
        return self._container_to_sandbox_info(container)

    def _destroy_container(self, sandbox: SandboxInfo) -> None:
        """Destroy a sandbox's container if it exists.

        Args:
            sandbox: The sandbox whose container to destroy.
        """
        if not sandbox.container_id:
            return

        try:
            container = self.client.containers.get(sandbox.container_id)
            # Stop if running
            if container.status == "running":
                container.stop()
            # Remove the container
            container.remove()
        except docker.errors.NotFound:
            # Container already gone
            pass

    def attach(
        self,
        name: str | None = None,
        workspace: str | None = None,
    ) -> Container:
        """Attach to an existing sandbox, starting it if stopped.

        Args:
            name: The sandbox name to attach to. If not provided, will
                auto-detect from workspace path.
            workspace: The workspace directory to find sandbox for.
                Defaults to current directory if name not provided.

        Returns:
            The Docker container object (running).

        Raises:
            ValueError: If sandbox not found.
            RuntimeError: If container cannot be started.
        """
        # Resolve the sandbox
        sandbox: SandboxInfo | None = None

        if name:
            sandbox = self.get_sandbox(name)
            if not sandbox:
                raise ValueError(f"Sandbox '{name}' not found")
        else:
            # Auto-detect from workspace path
            if workspace is None:
                workspace = os.getcwd()
            workspace = os.path.abspath(os.path.expanduser(workspace))
            sandbox = self.get_sandbox_for_path(workspace)
            if not sandbox:
                raise ValueError(
                    f"No sandbox found for workspace '{workspace}'. "
                    "Use 'sb create' to create one."
                )

        # Get the container
        if not sandbox.container_id:
            raise RuntimeError(
                f"Sandbox '{sandbox.name}' has no container ID. "
                "It may need to be recreated."
            )

        try:
            container = self.client.containers.get(sandbox.container_id)
        except docker.errors.NotFound:
            raise RuntimeError(
                f"Container for sandbox '{sandbox.name}' not found. "
                "It may need to be recreated."
            )

        # Start the container if not running
        if container.status != "running":
            container.start()
            # Refresh container state
            container.reload()

        return container

    def exec_shell(self, container: Container) -> int:
        """Execute an interactive shell in the container.

        This uses docker exec to attach to the container with an interactive
        zsh session. Note that this replaces the current process with docker.

        Args:
            container: The Docker container to exec into.

        Returns:
            Exit code from the shell session.
        """
        import subprocess

        uid, gid = self._get_uid_gid()

        # Use docker exec for interactive session
        # This is more reliable than the Docker SDK for TTY handling
        cmd = [
            "docker",
            "exec",
            "-it",
            "--user",
            f"{uid}:{gid}",
            container.id,
            "/bin/zsh",
        ]

        # Execute and replace current process behavior
        result = subprocess.run(cmd)
        return result.returncode

    def stop(
        self,
        name: str | None = None,
        workspace: str | None = None,
    ) -> SandboxInfo:
        """Stop a running sandbox.

        Args:
            name: The sandbox name to stop. If not provided, will
                auto-detect from workspace path.
            workspace: The workspace directory to find sandbox for.
                Defaults to current directory if name not provided.

        Returns:
            SandboxInfo for the stopped sandbox.

        Raises:
            ValueError: If sandbox not found.
            RuntimeError: If container cannot be stopped.
        """
        # Resolve the sandbox
        sandbox: SandboxInfo | None = None

        if name:
            sandbox = self.get_sandbox(name)
            if not sandbox:
                raise ValueError(f"Sandbox '{name}' not found")
        else:
            # Auto-detect from workspace path
            if workspace is None:
                workspace = os.getcwd()
            workspace = os.path.abspath(os.path.expanduser(workspace))
            sandbox = self.get_sandbox_for_path(workspace)
            if not sandbox:
                raise ValueError(
                    f"No sandbox found for workspace '{workspace}'. "
                    "Use 'sb create' to create one."
                )

        # Get the container
        if not sandbox.container_id:
            raise RuntimeError(
                f"Sandbox '{sandbox.name}' has no container ID. "
                "It may need to be recreated."
            )

        try:
            container = self.client.containers.get(sandbox.container_id)
        except docker.errors.NotFound:
            raise RuntimeError(
                f"Container for sandbox '{sandbox.name}' not found. "
                "It may need to be recreated."
            )

        # Stop the container if running
        if container.status == "running":
            container.stop()

        return sandbox

    def destroy(
        self,
        name: str | None = None,
        workspace: str | None = None,
        force: bool = False,
        confirm_callback: Callable[[str], bool] | None = None,
    ) -> SandboxInfo:
        """Destroy a sandbox, removing its container and state.

        Args:
            name: The sandbox name to destroy. If not provided, will
                auto-detect from workspace path.
            workspace: The workspace directory to find sandbox for.
                Defaults to current directory if name not provided.
            force: If True, skip confirmation prompt.
            confirm_callback: Function to call for user confirmation. Should
                accept a message string and return True if confirmed.

        Returns:
            SandboxInfo for the destroyed sandbox.

        Raises:
            ValueError: If sandbox not found or user cancels.
            RuntimeError: If container cannot be destroyed.
        """
        # Resolve the sandbox
        sandbox: SandboxInfo | None = None

        if name:
            sandbox = self.get_sandbox(name)
            if not sandbox:
                raise ValueError(f"Sandbox '{name}' not found")
        else:
            # Auto-detect from workspace path
            if workspace is None:
                workspace = os.getcwd()
            workspace = os.path.abspath(os.path.expanduser(workspace))
            sandbox = self.get_sandbox_for_path(workspace)
            if not sandbox:
                raise ValueError(
                    f"No sandbox found for workspace '{workspace}'. "
                    "Nothing to destroy."
                )

        # Confirm destruction unless force is set
        if not force:
            if confirm_callback:
                msg = (
                    f"Are you sure you want to destroy sandbox '{sandbox.name}'?\n"
                    "This will stop and remove the container. [y/N]:"
                )
                if not confirm_callback(msg):
                    raise ValueError("Sandbox destruction cancelled by user")
            else:
                raise ValueError(
                    f"Use --force to destroy sandbox '{sandbox.name}' without confirmation."
                )

        # Destroy the container and remove from state
        self._destroy_container(sandbox)

        return sandbox
