# Live classroom media load fixture

Disposable synthetic WebRTC load for an owned LiveKit server. This repository
contains only test source. It does not contain the application or server keys.

The standard public Ubuntu runner is used. No paid runner, artifact upload,
cache, recurring schedule, or production database is used.

Run the manual workflow with `LIVE_CLASS_LOAD_FIXTURE` supplied as an encrypted
repository secret. It contains only short-lived join tokens for two random
fixture rooms. Publishers cannot subscribe; receivers cannot publish video.
Receiver 0 may update its own metadata to expose RTP/RTCP measurements locally.
Remove this secret and the fixture rooms after the bounded test.

Synthetic publishers send 1920x1080 / 20 fps VP8 camera with 640x360 and 320x180
simulcast layers, plus the LiveKit CLI 2.18.8 Opus demo. The runner's existing
Chrome WebCodecs generates these images from canvas; it never captures a camera
or desktop. Receivers request the teacher's full camera and screen-share layers.
The worker reads real RTP but does not decode the received video; a separate
teacher browser verifies all student videos by decoding them.
