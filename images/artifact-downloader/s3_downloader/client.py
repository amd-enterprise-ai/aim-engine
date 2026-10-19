# MIT License
#
# Copyright (c) 2026 Advanced Micro Devices, Inc.
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

"""Environment-driven construction of a cloudpathlib S3Client.

Defaults fall back to boto3 behaviour (Signature V4, auto path-vs-virtual
addressing, and the standard credential chain). Validated end-to-end against
MinIO; AWS S3, Cloudflare R2, Backblaze B2, GCS and Ceph RGW use the same boto3
path and are expected to work. Optional AIM_S3_* knobs override region,
addressing style, signature version and multipart tuning when a backend needs
it.

Credentials resolve in one of three modes; see _credential_mode. The important
rule is that absent credentials are handed to boto3 rather than treated as a
public bucket, so IRSA and the other role-based sources keep working. Anonymous
access is opt-in via AIM_S3_ANONYMOUS or an "anonymous" sentinel key.
"""

import functools
import logging
import os
from typing import Optional

import boto3
from botocore import UNSIGNED
from botocore.config import Config
from boto3.s3.transfer import TransferConfig
from cloudpathlib import S3Client

logger = logging.getLogger(__name__)

# Credential values that mean "no real credentials" and select anonymous
# (unsigned) access. Besides the empty string, the literal "anonymous" is
# accepted because some configs set it as the access key to mean "no auth";
# under Signature V4 a literal key would otherwise be signed and rejected, so
# it must map to unsigned access instead. These apply only to a variable that is
# actually set -- an absent one means "resolve credentials normally".
_ANON_SENTINELS = {"", "anonymous"}

_TRUE_VALUES = {"1", "true", "yes", "on"}

# How the client authenticates, decided by _credential_mode().
_MODE_ANONYMOUS = "anonymous"  # unsigned requests, no credentials at all
_MODE_STATIC = "static"  # keys taken from the environment
_MODE_CHAIN = "chain"  # resolved by boto3 (IRSA, instance role, profile, ...)


def _getenv(*names: str) -> Optional[str]:
    """Return the first non-empty value among the given env var names.

    For each NAME, also accepts NAME_FILE pointing at a file (e.g. a mounted
    Secret) whose stripped contents are used, keeping secrets out of the env.
    """
    for name in names:
        value = os.environ.get(name)
        if value:
            return value
        file_path = os.environ.get(f"{name}_FILE")
        if file_path:
            try:
                with open(file_path) as f:
                    content = f.read().strip()
                if content:
                    return content
            except OSError as e:
                logger.warning(f"Could not read {name}_FILE ({file_path}): {e}")
    return None


def _env_is_set(name: str) -> bool:
    """Whether NAME (or its NAME_FILE variant) is present, even if empty.

    Telling "set to nothing" apart from "not set at all" is what lets an empty
    AWS_ACCESS_KEY_ID ask for anonymous access while an absent one falls through
    to boto3's credential chain. _getenv cannot express that difference because
    it collapses both to None.
    """
    return name in os.environ or f"{name}_FILE" in os.environ


def _is_truthy(value: Optional[str]) -> bool:
    return value is not None and value.strip().lower() in _TRUE_VALUES


def _is_anon(value: Optional[str]) -> bool:
    return value is None or value.strip().lower() in _ANON_SENTINELS


def _credential_mode(access_key_id: Optional[str], secret_access_key: Optional[str]) -> str:
    """Decide how to authenticate from the credential environment variables.

    Anonymous access has to be asked for explicitly, via AIM_S3_ANONYMOUS or an
    "anonymous"/empty sentinel in the key variables. Absent keys mean "resolve
    credentials the normal boto3 way", because that is precisely how IRSA, EKS
    Pod Identity, ECS task roles, EC2 instance profiles, shared profiles and
    credential processes present themselves: they all leave AWS_ACCESS_KEY_ID
    and AWS_SECRET_ACCESS_KEY unset on purpose. Reading that as "public bucket"
    and sending unsigned requests turns every one of them into an opaque 403.
    """
    explicit_mode = os.environ.get("AIM_S3_AUTH_MODE", "").strip().lower()
    if explicit_mode:
        if explicit_mode not in {_MODE_CHAIN, _MODE_STATIC, _MODE_ANONYMOUS}:
            raise ValueError(
                "AIM_S3_AUTH_MODE must be one of: chain, static, anonymous"
            )
        if explicit_mode == _MODE_STATIC and not (
            access_key_id and secret_access_key
        ):
            raise ValueError(
                "AIM_S3_AUTH_MODE=static requires both AWS_ACCESS_KEY_ID and "
                "AWS_SECRET_ACCESS_KEY"
            )
        return explicit_mode

    if _is_truthy(os.environ.get("AIM_S3_ANONYMOUS")):
        return _MODE_ANONYMOUS

    access_set = _env_is_set("AWS_ACCESS_KEY_ID")
    secret_set = _env_is_set("AWS_SECRET_ACCESS_KEY")

    # A sentinel in either variable is an explicit request for anonymous access.
    # Only one of the two needs it: configs that mean "no auth" often set just
    # the access key.
    if (access_set and _is_anon(access_key_id)) or (secret_set and _is_anon(secret_access_key)):
        real_access = access_key_id is not None and not _is_anon(access_key_id)
        real_secret = secret_access_key is not None and not _is_anon(secret_access_key)
        if real_access or real_secret:
            raise ValueError(
                "Contradictory S3 credentials: one of AWS_ACCESS_KEY_ID / "
                "AWS_SECRET_ACCESS_KEY asks for anonymous access while the other "
                "carries a real value. Set both to real values, set both to "
                "'anonymous', or set AIM_S3_ANONYMOUS=true."
            )
        return _MODE_ANONYMOUS

    if not access_set and not secret_set:
        return _MODE_CHAIN

    if access_key_id and secret_access_key:
        return _MODE_STATIC

    # Fail fast on partial credentials rather than letting boto3 raise an opaque
    # error deep inside the first request.
    missing = "AWS_ACCESS_KEY_ID" if not access_key_id else "AWS_SECRET_ACCESS_KEY"
    raise ValueError(
        f"Incomplete S3 credentials: {missing} is missing. Provide both "
        "AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY for authenticated access, "
        "set AIM_S3_ANONYMOUS=true for public buckets, or leave both unset to "
        "resolve credentials from the environment (IRSA, instance role, shared "
        "profile, ...)."
    )


def _ensure_scheme(endpoint_url: Optional[str]) -> Optional[str]:
    """If a custom endpoint has no URL scheme, derive one.

    boto3 requires a scheme on endpoint_url. Some configs supply a bare host
    (no scheme) and select http vs https via S3_NO_SSL / S3_USE_HTTPS; honour
    those so such configs keep working.
    """
    if not endpoint_url or "://" in endpoint_url:
        return endpoint_url
    no_ssl = (os.environ.get("S3_NO_SSL", "").lower() == "true") or (
        os.environ.get("S3_USE_HTTPS", "") == "0"
    )
    scheme = "http" if no_ssl else "https"
    logger.info(f"Endpoint '{endpoint_url}' has no scheme; assuming {scheme}://")
    return f"{scheme}://{endpoint_url}"


def _transfer_config() -> Optional[TransferConfig]:
    """Build a boto3 TransferConfig for multipart tuning, if overridden via env.

    Defaults to boto3's own TransferConfig (multipart, max_concurrency=10),
    which already gives parallel ranged downloads per object.
    """
    concurrency = _getenv("AIM_S3_MAX_CONCURRENCY")
    chunksize = _getenv("AIM_S3_MULTIPART_CHUNKSIZE_MB")
    if not concurrency and not chunksize:
        return None

    kwargs = {}
    if concurrency:
        kwargs["max_concurrency"] = int(concurrency)
    if chunksize:
        kwargs["multipart_chunksize"] = int(chunksize) * 1024 * 1024
    return TransferConfig(**kwargs)


def _verify_setting():
    """Return boto3's TLS verification setting.

    ``None`` preserves legacy botocore defaults, ``True`` explicitly selects
    botocore's default CA bundle, a path selects a custom CA bundle, and
    ``False`` is the explicitly requested insecure diagnostic mode.
    """
    insecure = os.environ.get("AIM_S3_INSECURE_SKIP_VERIFY", "").strip().lower()
    if insecure in _TRUE_VALUES:
        logger.warning(
            "TLS certificate and hostname verification is DISABLED for S3 "
            "requests; use only as a temporary diagnostic measure"
        )
        return False
    ca_bundle = _getenv("AWS_CA_BUNDLE", "AIM_S3_CA_BUNDLE")
    if ca_bundle:
        return ca_bundle
    if insecure in {"0", "false", "no", "off"}:
        # Typed connections always set an explicit false. Passing True prevents
        # an artifact-controlled AWS_CONFIG_FILE from replacing botocore's
        # default CA bundle through its ca_bundle setting.
        return True
    return None


def build_client() -> S3Client:
    """Construct an S3Client from the standard AWS_* (and AIM_S3_*) env vars.

    Recognised environment variables (each also accepts a NAME_FILE variant
    pointing at a mounted secret):
      - AWS_ENDPOINT_URL            custom endpoint (MinIO/Ceph/R2/B2/GCS/...)
      - AWS_ACCESS_KEY_ID           access key
      - AWS_SECRET_ACCESS_KEY       secret key
      - AWS_SESSION_TOKEN           session token (temporary credentials)
      - AIM_S3_ANONYMOUS            true to read a public bucket unauthenticated
      - AIM_S3_AUTH_MODE            chain | static | anonymous
      - AWS_REGION / AWS_DEFAULT_REGION   signing region
      - AIM_S3_ADDRESSING_STYLE     path | virtual | auto
      - AIM_S3_SIGNATURE_VERSION    e.g. s3v4 | s3
      - AWS_CA_BUNDLE               path to a PEM CA bundle used for TLS verification
      - AIM_S3_INSECURE_SKIP_VERIFY true disables TLS verification (unsafe)
      - AIM_S3_MAX_CONCURRENCY      per-object multipart threads
      - AIM_S3_MULTIPART_CHUNKSIZE_MB  multipart chunk size in MiB

    Leaving both key variables unset does NOT mean anonymous: boto3 resolves
    credentials itself (IRSA/web identity, container or instance role, shared
    profile, credential process). Anonymous access is opt-in -- see
    _credential_mode.
    """
    endpoint_url = _ensure_scheme(_getenv("AWS_ENDPOINT_URL"))
    access_key_id = _getenv("AWS_ACCESS_KEY_ID")
    secret_access_key = _getenv("AWS_SECRET_ACCESS_KEY")
    session_token = _getenv("AWS_SESSION_TOKEN")

    mode = _credential_mode(access_key_id, secret_access_key)
    if mode != _MODE_STATIC:
        # boto3 only consults its credential chain when no keys are passed in,
        # and UNSIGNED needs none either.
        access_key_id = secret_access_key = session_token = None
    if mode == _MODE_ANONYMOUS:
        logger.info("Anonymous S3 access requested; sending unsigned requests")
    elif mode == _MODE_CHAIN:
        logger.info(
            "No static S3 credentials set; resolving via the boto3 credential "
            "chain (web identity/IRSA, container or instance role, shared profile)"
        )

    # Region: most S3-compatible servers ignore it, but v4 signing needs a
    # value, so default to us-east-1 ONLY for custom endpoints. For real AWS
    # (no endpoint) leave it unset so boto3 resolves the bucket's region from
    # the credential chain / redirects instead of being pinned wrong.
    region = _getenv("AWS_REGION", "AWS_DEFAULT_REGION")
    if not region and endpoint_url:
        region = "us-east-1"

    # Assemble a botocore Config covering the bits cloudpathlib does not expose
    # directly. Anonymous access is expressed here (UNSIGNED).
    config_kwargs = {"retries": {"max_attempts": 8, "mode": "standard"}}
    if mode == _MODE_ANONYMOUS:
        config_kwargs["signature_version"] = UNSIGNED
    else:
        signature_version = _getenv("AIM_S3_SIGNATURE_VERSION")
        if signature_version:
            config_kwargs["signature_version"] = signature_version

    # Custom endpoints (MinIO, Ceph RGW, on-prem gateways) almost always need
    # path-style addressing because they lack per-bucket DNS. Default to it when
    # an endpoint is set; real AWS (no endpoint) keeps boto3's auto/virtual
    # behaviour. Users can force "virtual"/"auto" for backends that require it.
    addressing_style = _getenv("AIM_S3_ADDRESSING_STYLE")
    if not addressing_style and endpoint_url:
        addressing_style = "path"
    if addressing_style:
        config_kwargs["s3"] = {"addressing_style": addressing_style}

    config = Config(**config_kwargs)

    session = boto3.Session(
        aws_access_key_id=access_key_id,
        aws_secret_access_key=secret_access_key,
        aws_session_token=session_token,
        region_name=region,
    )

    # Resolve the chain up front so an environment with no credential source at
    # all reports that directly, instead of surfacing as whatever the first
    # request happens to return. Cost is one credential lookup, which for the
    # instance-metadata provider is a network round trip bounded by
    # AWS_METADATA_SERVICE_TIMEOUT (1s by default).
    if mode == _MODE_CHAIN and session.get_credentials() is None:
        raise ValueError(
            "Unable to locate credentials: no AWS_ACCESS_KEY_ID / "
            "AWS_SECRET_ACCESS_KEY were set and boto3 found no other credential "
            "source (web identity/IRSA, container or instance role, shared "
            "profile, credential process). Provide credentials, or set "
            "AIM_S3_ANONYMOUS=true if the bucket is public."
        )

    # cloudpathlib's S3Client calls sess.client("s3", endpoint_url=...) and
    # sess.resource(...) without a config when given a boto3_session. Binding
    # our config via functools.partial is the supported way to inject botocore
    # settings (see drivendataorg/cloudpathlib#435).
    client_kwargs = {"config": config}
    verify = _verify_setting()
    if verify is not None:
        client_kwargs["verify"] = verify
    session.client = functools.partial(session.client, **client_kwargs)
    session.resource = functools.partial(session.resource, **client_kwargs)

    return S3Client(
        boto3_session=session,
        endpoint_url=endpoint_url,
        boto3_transfer_config=_transfer_config(),
    )
