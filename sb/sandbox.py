"""Core sandbox management logic."""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING, Callable, NamedTuple

import docker
from docker.errors import DockerException

from sb.matching import find_matching_sandboxes
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


class MountSpec(NamedTuple):
    """Specification for a bind-mount into the sandbox container."""

    host: str
    container: str
    mode: str  # "ro" or "rw"


# Default mounts configuration
DEFAULT_MOUNTS: list[MountSpec] = [
    MountSpec(
        "~/.claude/", "/home/sandbox/.claude/", "rw"
    ),  # Claude Code needs write for debug logs
    MountSpec(
        "~/.claude.json", "/home/sandbox/.claude.json", "rw"
    ),  # Claude Code config
    MountSpec(
        "~/.config/claude-code/", "/home/sandbox/.config/claude-code/", "rw"
    ),  # Claude Code settings
    MountSpec("~/.codex/", "/home/sandbox/.codex/", "rw"),  # Codex needs write access
    MountSpec("~/.pi/", "/home/sandbox/.pi/", "rw"),  # Pi coding agent config & sessions
    MountSpec("~/.gitconfig", "/home/sandbox/.gitconfig", "ro"),
    # Shell configuration (user-editable)
    MountSpec("~/.config/sb/zshrc", "/home/sandbox/.zshrc", "ro"),
    MountSpec(
        "~/.config/sb/starship.toml", "/home/sandbox/.config/starship.toml", "ro"
    ),
    MountSpec(
        "~/.config/sb/nvim/", "/home/sandbox/.config/nvim/", "rw"
    ),  # rw for plugin install
]


def _map_container_path(host_path: str) -> str:
    """Map a host path to its container equivalent under /home/sandbox/.

    Only expands a leading ``~`` (via ``os.path.expanduser``) and computes
    the relative portion to place under ``/home/sandbox/``.  Paths that do
    not start with ``~`` are used as-is inside the container.
    """
    if host_path.startswith("~"):
        home = os.path.expanduser("~")
        expanded = os.path.expanduser(host_path)
        rel = os.path.relpath(expanded, home)
        return "/home/sandbox/" + rel
    return host_path


class ShellConfigManager:
    """Manages shell configuration files for sandbox containers.

    Ensures user-editable config files (zshrc, starship, nvim) exist in the
    config directory, copying defaults if needed.
    """

    def __init__(self, config_dir: Path | None = None) -> None:
        self._config_dir = config_dir or SHELL_CONFIG_DIR

    @staticmethod
    def _get_default_configs_dir() -> Path:
        """Get the path to the default config files shipped with sb."""
        return Path(__file__).parent / "docker" / "configs"

    def ensure_configs(self) -> None:
        """Ensure shell configuration files exist, copying defaults if needed.

        Creates the config directory and populates it with default configs
        for zshrc, starship.toml, and nvim if they don't already exist.
        """
        self._config_dir.mkdir(parents=True, exist_ok=True)
        defaults_dir = self._get_default_configs_dir()

        # Copy zshrc if missing
        zshrc_dest = self._config_dir / "zshrc"
        if not zshrc_dest.exists():
            zshrc_src = defaults_dir / "zshrc"
            if zshrc_src.exists():
                shutil.copy2(zshrc_src, zshrc_dest)

        # Copy starship.toml if missing
        starship_dest = self._config_dir / "starship.toml"
        if not starship_dest.exists():
            starship_src = defaults_dir / "starship.toml"
            if starship_src.exists():
                shutil.copy2(starship_src, starship_dest)

        # Copy nvim config directory if missing
        nvim_dest = self._config_dir / "nvim"
        if not nvim_dest.exists():
            nvim_src = defaults_dir / "nvim"
            if nvim_src.exists():
                shutil.copytree(nvim_src, nvim_dest)


class MountBuilder:
    """Builds Docker bind-mount lists for sandbox containers."""

    def __init__(self, extra_mounts: list[str] | None = None) -> None:
        self._extra_mounts = extra_mounts or []

    def build(
        self,
        workspace: str,
        extra_cli_mounts: list[str] | None = None,
    ) -> tuple[list[docker.types.Mount], list[str]]:
        """Construct mount list with appropriate ro/rw modes.

        Args:
            workspace: The workspace directory to mount read-write.
            extra_cli_mounts: Additional mounts from CLI (read-only).

        Returns:
            Tuple of (mounts, missing_paths) where missing_paths lists
            user-specified extra mount paths that do not exist on disk.
            Default mounts that are missing are silently skipped.
        """
        mounts: list[docker.types.Mount] = []
        missing: list[str] = []

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

        # Default config mounts (silently skip missing)
        for spec in DEFAULT_MOUNTS:
            expanded_host = os.path.expanduser(spec.host)
            if os.path.exists(expanded_host):
                mounts.append(
                    docker.types.Mount(
                        target=spec.container,
                        source=os.path.abspath(expanded_host),
                        type="bind",
                        read_only=(spec.mode == "ro"),
                    )
                )

        # Extra mounts from config (read-only, report missing)
        config_mounts, config_missing = self._mount_paths_readonly(self._extra_mounts)
        mounts.extend(config_mounts)
        missing.extend(config_missing)

        # Extra mounts from CLI (read-only, report missing)
        if extra_cli_mounts:
            cli_mounts, cli_missing = self._mount_paths_readonly(extra_cli_mounts)
            mounts.extend(cli_mounts)
            missing.extend(cli_missing)

        return mounts, missing

    @staticmethod
    def _mount_paths_readonly(
        paths: list[str],
    ) -> tuple[list[docker.types.Mount], list[str]]:
        """Build read-only Docker mounts from a list of host paths.

        Expands ``~``, skips paths that don't exist, and maps container
        paths via :func:`_map_container_path`.

        Args:
            paths: Host paths to mount (may contain leading ``~``).

        Returns:
            Tuple of (mounts, missing_paths) where missing_paths lists
            the original host paths that do not exist on disk.
        """
        mounts: list[docker.types.Mount] = []
        missing: list[str] = []
        for host_path in paths:
            expanded_host = os.path.expanduser(host_path)
            if os.path.exists(expanded_host):
                container_path = _map_container_path(host_path)
                mounts.append(
                    docker.types.Mount(
                        target=container_path,
                        source=os.path.abspath(expanded_host),
                        type="bind",
                        read_only=True,
                    )
                )
            else:
                missing.append(host_path)
        return mounts, missing


@dataclass
class SandboxInfo:
    """Information about a sandbox."""

    name: str
    workspace: str
    created_at: str
    container_id: str | None = None


class ImageManager:
    """Manages Docker image retrieval and building."""

    def __init__(self, client: DockerClient) -> None:
        self._client = client

    @staticmethod
    def _get_dockerfile_path() -> Path:
        """Get the path to the Dockerfile."""
        return Path(__file__).parent / "docker" / "Dockerfile"

    def ensure_image(self, image_name: str) -> Image:
        """Ensure a Docker image is available, building or pulling as needed.

        For the built-in image, builds from the bundled Dockerfile if not
        already present. For custom images, pulls from the registry.

        Args:
            image_name: The Docker image name/tag.

        Returns:
            The Docker image object.

        Raises:
            RuntimeError: If the image cannot be built or found.
        """
        # Check if image already exists
        try:
            return self._client.images.get(image_name)
        except docker.errors.ImageNotFound:
            pass

        # Build from bundled Dockerfile
        dockerfile_path = self._get_dockerfile_path()
        if not dockerfile_path.exists():
            raise RuntimeError(f"Dockerfile not found at {dockerfile_path}")

        docker_dir = dockerfile_path.parent
        image, _logs = self._client.images.build(
            path=str(docker_dir),
            tag=image_name,
            rm=True,
        )
        return image

    def ensure_custom_image(self, image_name: str) -> Image:
        """Ensure a custom (non-built-in) Docker image is available.

        Pulls from the registry if not already present locally.

        Args:
            image_name: The Docker image name/tag.

        Returns:
            The Docker image object.
        """
        try:
            return self._client.images.get(image_name)
        except docker.errors.ImageNotFound:
            return self._client.images.pull(image_name)


@dataclass
class SandboxManager:
    """Manages Docker sandbox containers."""

    image_name: str = DEFAULT_IMAGE_NAME
    extra_mounts: list[str] = field(default_factory=list)
    env_passthrough: list[str] = field(default_factory=list)
    custom_sensitive_dirs: list[str] = field(default_factory=list)

    _client: DockerClient | None = field(default=None, repr=False)
    _image_manager: ImageManager | None = field(default=None, init=False, repr=False)
    _mount_builder: MountBuilder = field(init=False, repr=False)
    _shell_config_mgr: ShellConfigManager = field(init=False, repr=False)

    def __post_init__(self) -> None:
        """Initialize the Docker client (if not injected) and collaborators."""
        if self._client is None:
            self._init_client()
        else:
            self._image_manager = ImageManager(self._client)
        self._mount_builder = MountBuilder(self.extra_mounts)
        self._shell_config_mgr = ShellConfigManager()

    def _init_client(self) -> None:
        """Initialize the Docker client."""
        try:
            self._client = docker.from_env()
            # Test connection
            self._client.ping()
        except DockerException as e:
            raise RuntimeError("Failed to connect to Docker. Is Docker running?") from e
        self._image_manager = ImageManager(self._client)

    @property
    def client(self) -> DockerClient:
        """Get the Docker client, raising if not initialized."""
        if self._client is None:
            raise RuntimeError("Docker client not initialized")
        return self._client

    @property
    def image_mgr(self) -> ImageManager:
        """Get the ImageManager, raising if not initialized."""
        if self._image_manager is None:
            raise RuntimeError("ImageManager not initialized")
        return self._image_manager

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

    def _check_sensitive_dir(self, path: str) -> str | None:
        """Check if path is a sensitive directory that should trigger a warning.

        Args:
            path: The filesystem path to check.

        Returns:
            The matched sensitive directory path if sensitive, None otherwise.
        """
        abs_path = os.path.abspath(os.path.expanduser(path))

        # Combine built-in and custom sensitive directories
        all_sensitive = SENSITIVE_DIRS | set(self.custom_sensitive_dirs)

        for sensitive_dir in all_sensitive:
            expanded_sensitive = os.path.abspath(os.path.expanduser(sensitive_dir))
            if abs_path == expanded_sensitive:
                return abs_path

        return None

    def _get_uid_gid(self) -> tuple[int, int]:
        """Get the current user's UID and GID."""
        return os.getuid(), os.getgid()

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

    def find_sandboxes(self, query: str) -> list[SandboxInfo]:
        """Find sandboxes matching a query using fuzzy matching.

        First tries exact match, then falls back to fuzzy matching.
        Returns list sorted by match quality (best first).

        Args:
            query: The search query (partial or full sandbox name).

        Returns:
            List of matching sandboxes, sorted by match quality.
        """
        sandboxes = self.list_sandboxes()
        return find_matching_sandboxes(query, sandboxes)

    def _resolve_sandbox(
        self,
        name: str | None = None,
        workspace: str | None = None,
        not_found_message: str | None = None,
    ) -> SandboxInfo:
        """Resolve a sandbox by name or workspace path.

        Args:
            name: The sandbox name to look up directly.
            workspace: The workspace directory to find sandbox for.
                Defaults to current directory if name not provided.
            not_found_message: Custom message for the ValueError when no
                sandbox is found by workspace path. Defaults to suggesting
                'sb create'.

        Returns:
            The resolved SandboxInfo.

        Raises:
            ValueError: If sandbox not found.
        """
        if name:
            sandbox = self.get_sandbox(name)
            if not sandbox:
                raise ValueError(f"Sandbox '{name}' not found")
            return sandbox

        # Auto-detect from workspace path
        if workspace is None:
            workspace = os.getcwd()
        workspace = os.path.abspath(os.path.expanduser(workspace))
        sandbox = self.get_sandbox_for_path(workspace)
        if not sandbox:
            msg = not_found_message or (
                f"No sandbox found for workspace '{workspace}'. "
                "Use 'sb create' to create one."
            )
            raise ValueError(msg)
        return sandbox

    def _get_container(self, sandbox: SandboxInfo) -> Container:
        """Get the Docker container for a sandbox.

        Args:
            sandbox: The sandbox whose container to retrieve.

        Returns:
            The Docker container object.

        Raises:
            RuntimeError: If the container has no ID or is not found.
        """
        if not sandbox.container_id:
            raise RuntimeError(
                f"Sandbox '{sandbox.name}' has no container ID. "
                "It may need to be recreated."
            )

        try:
            return self.client.containers.get(sandbox.container_id)
        except docker.errors.NotFound:
            raise RuntimeError(
                f"Container for sandbox '{sandbox.name}' not found. "
                "It may need to be recreated."
            )

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
        warn_callback: Callable[[str], None] | None = None,
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
            warn_callback: Function to call for non-fatal warnings (e.g.
                missing mount paths). Accepts a warning message string.

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
                        "Do you want to recreate it?"
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
        sensitive_path = self._check_sensitive_dir(workspace)
        if sensitive_path and not force:
            if confirm_callback:
                msg = (
                    f"Warning: Creating sandbox with access to '{sensitive_path}' is "
                    f"potentially dangerous.\n"
                    f"This gives the sandbox write access to this directory.\n"
                    f"Continue?"
                )
                if not confirm_callback(msg):
                    raise ValueError("Sandbox creation cancelled by user")
            else:
                raise ValueError(
                    "Workspace is a sensitive directory. Use --force to override."
                )

        # Ensure shell configs exist (copy defaults if needed)
        self._shell_config_mgr.ensure_configs()

        # Build or get the image
        image_name = image or self.image_name
        if image:
            self.image_mgr.ensure_custom_image(image_name)
        else:
            self.image_mgr.ensure_image(image_name)

        # Build mounts
        mounts, missing_mounts = self._mount_builder.build(workspace, extra_mounts)

        # Warn about missing user-specified mount paths
        if missing_mounts and warn_callback:
            for path in missing_mounts:
                warn_callback(f"Mount path does not exist, skipping: {path}")

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
        sandbox = self._resolve_sandbox(name=name, workspace=workspace)
        container = self._get_container(sandbox)

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
        # Note: We run as root and use setpriv to drop privileges because
        # docker exec --user doesn't initialize supplementary groups from
        # /etc/group. The --init-groups flag ensures wheel group membership.
        # We also set HOME/USER explicitly since we start as root.
        cmd = [
            "docker",
            "exec",
            "-it",
            "-e",
            "HOME=/home/sandbox",
            "-e",
            "USER=sandbox",
            "-w",
            "/home/sandbox/workspace",
            container.id,
            "setpriv",
            "--reuid",
            str(uid),
            "--regid",
            str(gid),
            "--init-groups",
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
        sandbox = self._resolve_sandbox(name=name, workspace=workspace)
        container = self._get_container(sandbox)

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
        # Resolve workspace for error message before _resolve_sandbox consumes it
        resolved_ws = os.path.abspath(os.path.expanduser(workspace or os.getcwd()))
        sandbox = self._resolve_sandbox(
            name=name,
            workspace=workspace,
            not_found_message=(
                f"No sandbox found for workspace '{resolved_ws}'. Nothing to destroy."
            ),
        )

        # Confirm destruction unless force is set
        if not force:
            if confirm_callback:
                msg = (
                    f"Are you sure you want to destroy sandbox '{sandbox.name}'?\n"
                    "This will stop and remove the container."
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
