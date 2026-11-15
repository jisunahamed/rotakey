"""Refresh only reviewed model identities from NVIDIA's official model pages."""
import concurrent.futures
import datetime
import json
import pathlib
import re
import urllib.request

MODELS = [
    'deepseek-ai/deepseek-v4.1-flash', 'google/diffusiongemma-26b-a4b-it',
    'google/gemma-4-31b-it', 'meta/muse-glimmer-30b', 'minimaxai/minimax-m3.1-flash',
    'mistralai/mistral-large-4-1t-a52b', 'nvidia/ising-calibration-1.5-31b',
    'nvidia/nemotron-3-nano-omni-30b-a3b-reasoning', 'nvidia/nemotron-3-ultra-550b-a55b',
    'nvidia/nemotron-3.5-bolt-e4b', 'nvidia/nemotron-3.5-lightning-30b-a3b',
    'openai/gpt-oss-20b', 'poolside/laguna-xs-2.1', 'stepfun-ai/step-5-preview', 'z-ai/glm-5.3-flash',
]

def fetch(model):
    # NVIDIA uses a URL slug for GLM but a different inference ID.
    url = 'https://build.nvidia.com/' + model.replace('z-ai/glm-5.3-flash', 'z-ai/glm-5-3-flash')
    raw = urllib.request.urlopen(url, timeout=45).read().decode().replace('\\"', '"')
    match = re.search(r'"specifications":(\{.*?\}),"modelCapability":(\{.*?\})', raw)
    if not match:
        raise ValueError(f'{model}: NVIDIA specification object not found')
    spec, caps = map(json.loads, match.groups())
    context = spec['contextLength']
    assert isinstance(context, int) and 1024 <= context <= 100_000_000
    return dict(id=model, source_url=url, context_window=context,
                input_modalities=spec['inputModalities'],
                tools=caps.get('functionCalling'), reasoning=caps.get('reasoning'),
                structured_output=caps.get('structuredOutput'))

if __name__ == '__main__':
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as executor:
        entries = list(executor.map(fetch, MODELS))
    dest = pathlib.Path(__file__).resolve().parents[1] / 'internal/app/catalog/nvidia.json'
    dest.parent.mkdir(exist_ok=True)
    dest.write_text(json.dumps({'checked_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                               'models': entries}, indent=2) + '\n', encoding='utf-8')
    for model in entries:
        print(model['id'], model['context_window'])
