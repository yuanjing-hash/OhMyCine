/*
 * Derived from mpv 9ce79bc's video/out/opengl/context_android.c.
 *
 * mpv is free software; you can redistribute it and/or modify it under the
 * terms of the GNU Lesser General Public License as published by the Free
 * Software Foundation; either version 2.1 of the License, or (at your option)
 * any later version. mpv is distributed without any warranty.
 * See the GNU Lesser General Public License for more details.
 */

#include <EGL/egl.h>
#include <EGL/eglext.h>
#include <libplacebo/colorspace.h>
#include <string.h>

#include "common/common.h"
#include "common/global.h"
#include "player/client.h"
#include "video/mp_image.h"
#include "video/out/android_common.h"
#include "video/out/gpu/ra.h"
#include "egl_helpers.h"
#include "context.h"

struct priv {
    struct GL gl;
    EGLDisplay egl_display;
    EGLContext egl_context;
    EGLSurface egl_surface;
    EGLConfig egl_config;
    EGLint surface_colorspace;
    int color_depth;
    struct pl_color_space color;
    bool surface_failed;
};

static bool has_extension(const char *extensions, const char *name)
{
    if (!extensions || !name || !*name)
        return false;
    size_t length = strlen(name);
    const char *at = extensions;
    while ((at = strstr(at, name))) {
        if ((at == extensions || at[-1] == ' ') &&
            (at[length] == ' ' || at[length] == '\0'))
            return true;
        at += length;
    }
    return false;
}

static int config_depth(EGLDisplay display, EGLConfig config)
{
    EGLint r = 0, g = 0, b = 0;
    if (!eglGetConfigAttrib(display, config, EGL_RED_SIZE, &r) ||
        !eglGetConfigAttrib(display, config, EGL_GREEN_SIZE, &g) ||
        !eglGetConfigAttrib(display, config, EGL_BLUE_SIZE, &b))
        return 0;
    return MPMIN(r, MPMIN(g, b));
}

static int refine_config(void *user_data, EGLConfig *configs, int count)
{
    struct priv *p = user_data;
    int chosen = 0, score = -1;
    for (int i = 0; i < count; i++) {
        int depth = config_depth(p->egl_display, configs[i]);
        // Prefer a native ten-bit carrier, then FP16. An eight-bit config
        // remains usable for SDR, but set_color must refuse it for HDR.
        int candidate = depth == 10 ? 3 : depth >= 16 ? 2 : depth >= 8 ? 1 : 0;
        if (candidate > score) {
            score = candidate;
            chosen = i;
        }
    }
    return chosen;
}

static bool high_precision_fbo(struct ra_ctx *ctx)
{
    for (int i = 0; i < ctx->ra->num_formats; i++) {
        const struct ra_format *fmt = ctx->ra->formats[i];
        if (fmt->ctype == RA_CTYPE_FLOAT && fmt->renderable &&
            fmt->num_components == 4 && fmt->component_depth[0] >= 16 &&
            fmt->component_depth[1] >= 16 && fmt->component_depth[2] >= 16 &&
            fmt->component_depth[3] >= 16)
            return true;
    }
    return false;
}

static bool read_surface_color(struct ra_ctx *ctx, EGLint requested)
{
    struct priv *p = ctx->priv;
    const char *exts = eglQueryString(p->egl_display, EGL_EXTENSIONS);
    EGLint actual = EGL_GL_COLORSPACE_SRGB_KHR;
    if (has_extension(exts, "EGL_KHR_gl_colorspace") &&
        !eglQuerySurface(p->egl_display, p->egl_surface,
                         EGL_GL_COLORSPACE_KHR, &actual))
        return false;
    if (actual != requested)
        return false;
    struct pl_color_space color = {0};
    switch (actual) {
    case EGL_GL_COLORSPACE_BT2020_PQ_EXT:
        color = pl_color_space_hdr10;
        break;
    case EGL_GL_COLORSPACE_BT2020_HLG_EXT:
        color = (struct pl_color_space){
            .primaries = PL_COLOR_PRIM_BT_2020,
            .transfer = PL_COLOR_TRC_HLG,
        };
        break;
    case EGL_GL_COLORSPACE_SRGB_KHR:
        color = pl_color_space_srgb;
        break;
    default:
        return false;
    }
    p->surface_colorspace = actual;
    p->color = color;
    p->color_depth = config_depth(p->egl_display, p->egl_config);
    MP_VERBOSE(ctx, "OHMYCINE_ANDROID_HDR_EGL_V1 colorspace=0x%x component-bits=%d\n",
               actual, p->color_depth);
    return true;
}

static bool create_surface(struct ra_ctx *ctx, EGLint colorspace)
{
    struct priv *p = ctx->priv;
    ANativeWindow *window = vo_android_native_window(ctx->vo);
    if (!window)
        return false;
    const char *exts = eglQueryString(p->egl_display, EGL_EXTENSIONS);
    bool has_colorspace = has_extension(exts, "EGL_KHR_gl_colorspace");
    if (!has_colorspace && colorspace != EGL_GL_COLORSPACE_SRGB_KHR)
        return false;
    if (p->egl_surface != EGL_NO_SURFACE) {
        eglMakeCurrent(p->egl_display, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
        eglDestroySurface(p->egl_display, p->egl_surface);
        p->egl_surface = EGL_NO_SURFACE;
    }
    p->color = (struct pl_color_space){0};
    EGLint format = 0;
    if (!eglGetConfigAttrib(p->egl_display, p->egl_config, EGL_NATIVE_VISUAL_ID, &format) ||
        ANativeWindow_setBuffersGeometry(window, 0, 0, format) != 0)
        return false;
    const EGLint attributes[] = {EGL_GL_COLORSPACE_KHR, colorspace, EGL_NONE};
    p->egl_surface = eglCreateWindowSurface(p->egl_display, p->egl_config,
        (EGLNativeWindowType)window, has_colorspace ? attributes : NULL);
    if (p->egl_surface == EGL_NO_SURFACE ||
        !eglMakeCurrent(p->egl_display, p->egl_surface, p->egl_surface, p->egl_context))
        return false;
    return read_surface_color(ctx, colorspace);
}

static bool android_set_color(struct ra_ctx *ctx, struct mp_image_params *params)
{
    struct priv *p = ctx->priv;
    if (p->surface_failed)
        return false;
    enum pl_color_transfer transfer = params ? params->color.transfer : PL_COLOR_TRC_SRGB;
    EGLint desired = EGL_GL_COLORSPACE_SRGB_KHR;
    const char *required = NULL;
    if (transfer == PL_COLOR_TRC_PQ) {
        desired = EGL_GL_COLORSPACE_BT2020_PQ_EXT;
        required = "EGL_EXT_gl_colorspace_bt2020_pq";
    } else if (transfer == PL_COLOR_TRC_HLG) {
        desired = EGL_GL_COLORSPACE_BT2020_HLG_EXT;
        required = "EGL_EXT_gl_colorspace_bt2020_hlg";
    }
    const char *exts = eglQueryString(p->egl_display, EGL_EXTENSIONS);
    if (required && (p->gl.es < 300 || config_depth(p->egl_display, p->egl_config) < 10 ||
        !has_extension(exts, required) || !high_precision_fbo(ctx)))
        goto fail;
    if (p->surface_colorspace != desired && !create_surface(ctx, desired))
        goto fail;
    if (params)
        params->color = p->color;
    return true;
fail:
    p->surface_failed = true;
    p->color = (struct pl_color_space){0};
    MP_ERR(ctx, "Android HDR EGL surface negotiation failed (EGL 0x%x).\n", eglGetError());
    // This is an actual rejected Surface, not a log-driven decision. The
    // existing core API invalidates the VO and propagates its native error.
    kill_video_async(ctx->vo->global->client_api);
    return false;
}

static pl_color_space_t android_preferred_csp(struct ra_ctx *ctx)
{
    return ((struct priv *)ctx->priv)->color;
}

static int android_color_depth(struct ra_ctx *ctx)
{
    return ((struct priv *)ctx->priv)->color_depth;
}

static void android_swap_buffers(struct ra_ctx *ctx)
{
    struct priv *p = ctx->priv;
    if (!p->surface_failed && !eglSwapBuffers(p->egl_display, p->egl_surface)) {
        p->surface_failed = true;
        p->color = (struct pl_color_space){0};
        MP_ERR(ctx, "Android EGL presentation failed (EGL 0x%x).\n", eglGetError());
        kill_video_async(ctx->vo->global->client_api);
    }
}

static void android_uninit(struct ra_ctx *ctx)
{
    struct priv *p = ctx->priv;
    ra_gl_ctx_uninit(ctx);
    if (p->egl_surface != EGL_NO_SURFACE) {
        eglMakeCurrent(p->egl_display, EGL_NO_SURFACE, EGL_NO_SURFACE, EGL_NO_CONTEXT);
        eglDestroySurface(p->egl_display, p->egl_surface);
    }
    if (p->egl_context != EGL_NO_CONTEXT)
        eglDestroyContext(p->egl_display, p->egl_context);
    vo_android_uninit(ctx->vo);
}

static bool android_init(struct ra_ctx *ctx)
{
    struct priv *p = ctx->priv = talloc_zero(ctx, struct priv);
    if (!vo_android_init(ctx->vo))
        goto fail;
    p->egl_display = eglGetDisplay(EGL_DEFAULT_DISPLAY);
    if (!eglInitialize(p->egl_display, NULL, NULL)) {
        MP_FATAL(ctx, "EGL failed to initialize.\n");
        goto fail;
    }
    if (!mpegl_create_context_cb(ctx, p->egl_display,
            (struct mpegl_cb){.refine_config = refine_config, .user_data = p},
            &p->egl_context, &p->egl_config) ||
        !create_surface(ctx, EGL_GL_COLORSPACE_SRGB_KHR)) {
        MP_FATAL(ctx, "Android EGL context or Surface creation failed.\n");
        goto fail;
    }
    mpegl_load_functions(&p->gl, ctx->log);
    struct ra_ctx_params params = {
        .swap_buffers = android_swap_buffers,
        .set_color = android_set_color,
        .preferred_csp = android_preferred_csp,
        .color_depth = android_color_depth,
    };
    if (!ra_gl_ctx_init(ctx, &p->gl, params))
        goto fail;
    return true;
fail:
    android_uninit(ctx);
    return false;
}

static bool android_reconfig(struct ra_ctx *ctx)
{
    int w, h;
    if (!vo_android_surface_size(ctx->vo, &w, &h))
        return false;
    struct priv *p = ctx->priv;
    if (p->surface_failed)
        return false;
    ANativeWindow *window = vo_android_native_window(ctx->vo);
    if (window) {
        int32_t format = ANativeWindow_getFormat(window);
        if (ANativeWindow_setBuffersGeometry(window, w, h, format) != 0)
            return false;
    }
    ctx->vo->dwidth = w;
    ctx->vo->dheight = h;
    ra_gl_ctx_resize(ctx->swapchain, w, h, 0);
    return true;
}

static int android_control(struct ra_ctx *ctx, int *events, int request, void *arg)
{
    return VO_NOTIMPL;
}

const struct ra_ctx_fns ra_ctx_android = {
    .type = "opengl",
    .name = "android",
    .description = "Android/EGL HDR",
    .reconfig = android_reconfig,
    .control = android_control,
    .init = android_init,
    .uninit = android_uninit,
};
