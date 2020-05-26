# livepeer-vod-tool
A VOD Transcoding Tool Using Livepeer's Hosted Gateway

This tool uses `ffmpeg` to segment a video into HLS, and then uses the Livepeer gateway API to transcode each `.ts` segment based on a preset.  You can get an API key from the [Livepeer website](https://livepeer.com)

To try this out, you can build the project yourself, or download one of the releases.

Try the following command:

`./main -file bbb_30s.mp4 -apiKey {apiKey} -presets bbbPresets.json`

You should see a `/results` directory that contains your transcoding results.  You can play the transcoded result by running `ffplay results/playlist.m3u8`
