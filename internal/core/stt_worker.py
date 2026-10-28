import base64
import json
import sys

import numpy as np
from faster_whisper import WhisperModel

model_name = sys.argv[1] if len(sys.argv) > 1 else "small"
language = sys.argv[2] if len(sys.argv) > 2 else "ru"
device = sys.argv[3] if len(sys.argv) > 3 else "auto"
if not language:
    language = None

model = WhisperModel(model_name, device=device, compute_type="auto")

for line in sys.stdin:
    try:
        req = json.loads(line)
        data = base64.b64decode(req.get("pcm", ""))
        audio = np.frombuffer(data, dtype="<i2").astype(np.float32) / 32768.0
        try:
            segments, _ = model.transcribe(audio, language=language, beam_size=5, vad_filter=True)
        except Exception:
            segments, _ = model.transcribe(audio, language=language, beam_size=5, vad_filter=False)
        text = " ".join(s.text.strip() for s in segments).strip()
        print(json.dumps({"text": text}), flush=True)
    except Exception as exc:
        print(json.dumps({"error": str(exc)}), flush=True)
