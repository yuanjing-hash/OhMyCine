// Compiled against the exact headers used by the vendored mpv-android runtime.
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
#include <libavutil/avutil.h>

typedef struct Input { AVFormatContext *ctx; AVPacket *packet; int map[2]; int count; int eof; } Input;

static int open_input(Input *input, const char *path, AVIOInterruptCB interrupt, int video_only, int audio_only) {
    AVDictionary *options = NULL;
    int result;
    input->ctx = avformat_alloc_context();
    if (!input->ctx) return AVERROR(ENOMEM);
    input->ctx->interrupt_callback = interrupt;
    av_dict_set(&options, "protocol_whitelist", "file", 0);
    av_dict_set(&options, "format_whitelist", "mov,mpegts,matroska", 0);
    av_dict_set(&options, "enable_drefs", "0", 0);
    result = avformat_open_input(&input->ctx, path, NULL, &options);
    av_dict_free(&options);
    if (result < 0 || (result = avformat_find_stream_info(input->ctx, NULL)) < 0) return result;
    input->map[0] = audio_only ? -1 : av_find_best_stream(input->ctx, AVMEDIA_TYPE_VIDEO, -1, -1, NULL, 0);
    input->map[1] = video_only ? -1 : av_find_best_stream(input->ctx, AVMEDIA_TYPE_AUDIO, -1, -1, NULL, 0);
    if ((!audio_only && input->map[0] < 0) || (audio_only && input->map[1] < 0)) return AVERROR_STREAM_NOT_FOUND;
    input->packet = av_packet_alloc();
    return input->packet ? 0 : AVERROR(ENOMEM);
}

static int next_packet(Input *input, AVIOInterruptCB interrupt) {
    int result;
    av_packet_unref(input->packet);
    while ((result = av_read_frame(input->ctx, input->packet)) >= 0) {
        if (interrupt.callback(interrupt.opaque)) return AVERROR_EXIT;
        if (input->packet->stream_index == input->map[0] || input->packet->stream_index == input->map[1]) return 0;
        av_packet_unref(input->packet);
    }
    if (result == AVERROR_EOF) { input->eof = 1; return 0; }
    return result;
}

int ohmycine_remux(const char *video, const char *audio, const char *output, int (*interrupted)(void *), void *opaque) {
    Input inputs[2] = {0};
    AVFormatContext *out = NULL;
    AVIOInterruptCB interrupt = { interrupted, opaque };
    int mappings[2][2] = {{-1,-1},{-1,-1}};
    int count = audio ? 2 : 1;
    int result = 0;
    int header_written = 0;
    // Fail closed rather than interpreting a different ABI's public struct layout.
    if (avformat_version() != AV_VERSION_INT(62,13,102) || avcodec_version() != AV_VERSION_INT(62,30,100)) return -1;
    if ((result = open_input(&inputs[0], video, interrupt, audio != NULL, 0)) < 0) goto end;
    if (audio && (result = open_input(&inputs[1], audio, interrupt, 0, 1)) < 0) goto end;
    if ((result = avformat_alloc_output_context2(&out, NULL, NULL, output)) < 0) goto end;
    if (!out) { result = AVERROR(ENOMEM); goto end; }
    out->interrupt_callback = interrupt;
    for (int i = 0; i < count; i++) {
        for (int kind = 0; kind < 2; kind++) {
            int index = inputs[i].map[kind];
            if (index < 0) continue;
            AVStream *stream = avformat_new_stream(out, NULL);
            if (!stream) { result = AVERROR(ENOMEM); goto end; }
            mappings[i][kind] = stream->index;
            if ((result = avcodec_parameters_copy(stream->codecpar, inputs[i].ctx->streams[index]->codecpar)) < 0) goto end;
            stream->codecpar->codec_tag = 0;
            stream->time_base = inputs[i].ctx->streams[index]->time_base;
        }
    }
    if ((result = avio_open2(&out->pb, output, AVIO_FLAG_WRITE, &interrupt, NULL)) < 0) goto end;
    if ((result = avformat_write_header(out, NULL)) < 0) goto end;
    header_written = 1;
    for (int i = 0; i < count; i++) if ((result = next_packet(&inputs[i], interrupt)) < 0) goto end;
    while (!inputs[0].eof || (count == 2 && !inputs[1].eof)) {
        int selected = 0;
        if (interrupted(opaque)) { result = AVERROR_EXIT; goto end; }
        if (count == 2) {
            if (inputs[0].eof) selected = 1;
            else if (!inputs[1].eof) {
                AVPacket *a = inputs[0].packet, *b = inputs[1].packet;
                if (av_compare_ts(a->dts, inputs[0].ctx->streams[a->stream_index]->time_base, b->dts, inputs[1].ctx->streams[b->stream_index]->time_base) > 0) selected = 1;
            }
        }
        Input *input = &inputs[selected];
        AVPacket *packet = input->packet;
        int kind = packet->stream_index == input->map[0] ? 0 : 1;
        AVStream *source = input->ctx->streams[packet->stream_index];
        AVStream *destination = out->streams[mappings[selected][kind]];
        av_packet_rescale_ts(packet, source->time_base, destination->time_base);
        packet->stream_index = destination->index;
        packet->pos = -1;
        if ((result = av_interleaved_write_frame(out, packet)) < 0 || (result = next_packet(input, interrupt)) < 0) goto end;
    }
    result = av_write_trailer(out);
end:
    for (int i = 0; i < count; i++) { av_packet_free(&inputs[i].packet); avformat_close_input(&inputs[i].ctx); }
    if (out) { if (out->pb) avio_closep(&out->pb); avformat_free_context(out); }
    (void)header_written;
    return result;
}
