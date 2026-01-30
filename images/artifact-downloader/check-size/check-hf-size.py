import os
import sys
from huggingface_hub import HfApi
from huggingface_hub.utils import RepositoryNotFoundError, GatedRepoError
MODEL_PATH = os.environ['MODEL_PATH']
try:
    info = HfApi().model_info(MODEL_PATH, files_metadata=True)
    print(sum(f.size or 0 for f in info.siblings))
except RepositoryNotFoundError:
    print(f'Repository Not Found: {MODEL_PATH}', file=sys.stderr)
    print('Check the model name or ensure it exists on HuggingFace.', file=sys.stderr)
    sys.exit(1)
except GatedRepoError:
    print(f'Model requires authentication: {MODEL_PATH}', file=sys.stderr)
    print('Set HF_TOKEN environment variable with a valid HuggingFace token.', file=sys.stderr)
    print(f'Cannot access gated repo: {MODEL_PATH}', file=sys.stderr)
    sys.exit(2)
except Exception as e:
    print(f'Failed to fetch model info: {e}', file=sys.stderr)
    sys.exit(3)
