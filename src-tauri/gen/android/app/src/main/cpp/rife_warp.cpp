// SPDX-License-Identifier: MIT
// The sampling semantics and Vulkan kernels are derived from
// nihui/rife-ncnn-vulkan commit a7532fc3f9f8f008cd6eecd6f2ffe2a9698e0cf7.

#include "rife_warp.h"

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <vector>

namespace {

constexpr char kWarpPack1[] = R"glsl(
#version 450
#if NCNN_fp16_storage
#extension GL_EXT_shader_16bit_storage: require
#endif
#if NCNN_fp16_arithmetic
#extension GL_EXT_shader_explicit_arithmetic_types_float16: require
#endif
layout(binding=0) readonly buffer Image { sfp values[]; } image_blob;
layout(binding=1) readonly buffer Flow { sfp values[]; } flow_blob;
layout(binding=2) writeonly buffer Output { sfp values[]; } top_blob;
layout(push_constant) uniform Parameters { int w; int h; int c; int cstep; } p;
void main() {
    int x=int(gl_GlobalInvocationID.x), y=int(gl_GlobalInvocationID.y), z=int(gl_GlobalInvocationID.z);
    if (x>=p.w || y>=p.h || z>=p.c) return;
    afp sx=afp(x)+buffer_ld1(flow_blob.values,y*p.w+x);
    afp sy=afp(y)+buffer_ld1(flow_blob.values,p.cstep+y*p.w+x);
    int x0=int(floor(sx)), y0=int(floor(sy)), x1=x0+1, y1=y0+1;
    x0=clamp(x0,0,p.w-1); y0=clamp(y0,0,p.h-1);
    x1=clamp(x1,0,p.w-1); y1=clamp(y1,0,p.h-1);
    afp ax=sx-afp(x0), ay=sy-afp(y0);
    int base=z*p.cstep;
    afp v0=buffer_ld1(image_blob.values,base+y0*p.w+x0);
    afp v1=buffer_ld1(image_blob.values,base+y0*p.w+x1);
    afp v2=buffer_ld1(image_blob.values,base+y1*p.w+x0);
    afp v3=buffer_ld1(image_blob.values,base+y1*p.w+x1);
    afp a=mix(v0,v1,ax), b=mix(v2,v3,ax);
    buffer_st1(top_blob.values,base+y*p.w+x,mix(a,b,ay));
}
)glsl";

constexpr char kWarpPack4[] = R"glsl(
#version 450
#if NCNN_fp16_storage
#extension GL_EXT_shader_16bit_storage: require
#endif
#if NCNN_fp16_arithmetic
#extension GL_EXT_shader_explicit_arithmetic_types_float16: require
#endif
layout(binding=0) readonly buffer Image { sfpvec4 values[]; } image_blob;
layout(binding=1) readonly buffer Flow { sfp values[]; } flow_blob;
layout(binding=2) writeonly buffer Output { sfpvec4 values[]; } top_blob;
layout(push_constant) uniform Parameters { int w; int h; int c; int cstep; } p;
void main() {
    int x=int(gl_GlobalInvocationID.x), y=int(gl_GlobalInvocationID.y), z=int(gl_GlobalInvocationID.z);
    if (x>=p.w || y>=p.h || z>=p.c) return;
    afp sx=afp(x)+buffer_ld1(flow_blob.values,y*p.w+x);
    afp sy=afp(y)+buffer_ld1(flow_blob.values,p.cstep+y*p.w+x);
    int x0=int(floor(sx)), y0=int(floor(sy)), x1=x0+1, y1=y0+1;
    x0=clamp(x0,0,p.w-1); y0=clamp(y0,0,p.h-1);
    x1=clamp(x1,0,p.w-1); y1=clamp(y1,0,p.h-1);
    afp ax=sx-afp(x0), ay=sy-afp(y0); int base=z*p.cstep;
    afpvec4 v0=buffer_ld4(image_blob.values,base+y0*p.w+x0);
    afpvec4 v1=buffer_ld4(image_blob.values,base+y0*p.w+x1);
    afpvec4 v2=buffer_ld4(image_blob.values,base+y1*p.w+x0);
    afpvec4 v3=buffer_ld4(image_blob.values,base+y1*p.w+x1);
    buffer_st4(top_blob.values,base+y*p.w+x,mix(mix(v0,v1,ax),mix(v2,v3,ax),ay));
}
)glsl";

constexpr char kWarpPack8[] = R"glsl(
#version 450
#if NCNN_fp16_storage
#extension GL_EXT_shader_16bit_storage: require
struct sfpvec8 { f16vec4 lo; f16vec4 hi; };
#endif
#if NCNN_fp16_arithmetic
#extension GL_EXT_shader_explicit_arithmetic_types_float16: require
#endif
layout(binding=0) readonly buffer Image { sfpvec8 values[]; } image_blob;
layout(binding=1) readonly buffer Flow { sfp values[]; } flow_blob;
layout(binding=2) writeonly buffer Output { sfpvec8 values[]; } top_blob;
layout(push_constant) uniform Parameters { int w; int h; int c; int cstep; } p;
void main() {
    int x=int(gl_GlobalInvocationID.x), y=int(gl_GlobalInvocationID.y), z=int(gl_GlobalInvocationID.z);
    if (x>=p.w || y>=p.h || z>=p.c) return;
    afp sx=afp(x)+buffer_ld1(flow_blob.values,y*p.w+x);
    afp sy=afp(y)+buffer_ld1(flow_blob.values,p.cstep+y*p.w+x);
    int x0=int(floor(sx)), y0=int(floor(sy)), x1=x0+1, y1=y0+1;
    x0=clamp(x0,0,p.w-1); y0=clamp(y0,0,p.h-1);
    x1=clamp(x1,0,p.w-1); y1=clamp(y1,0,p.h-1);
    afp ax=sx-afp(x0), ay=sy-afp(y0); int base=z*p.cstep;
    afpvec8 v0=buffer_ld8(image_blob.values,base+y0*p.w+x0);
    afpvec8 v1=buffer_ld8(image_blob.values,base+y0*p.w+x1);
    afpvec8 v2=buffer_ld8(image_blob.values,base+y1*p.w+x0);
    afpvec8 v3=buffer_ld8(image_blob.values,base+y1*p.w+x1);
    afpvec8 a; afpvec8 b; afpvec8 v;
    a[0]=mix(v0[0],v1[0],ax); a[1]=mix(v0[1],v1[1],ax);
    b[0]=mix(v2[0],v3[0],ax); b[1]=mix(v2[1],v3[1],ax);
    v[0]=mix(a[0],b[0],ay); v[1]=mix(a[1],b[1],ay);
    buffer_st8(top_blob.values,base+y*p.w+x,v);
}
)glsl";

int compile_pipeline(
    const ncnn::VulkanDevice* device,
    const ncnn::Option& options,
    const char* source,
    size_t source_size,
    ncnn::Pipeline*& pipeline) {
    std::vector<uint32_t> spirv;
    // ncnn's sized overload expects the GLSL byte count without the trailing
    // NUL. Passing sizeof(char[]) made the custom layer dependent on glslang's
    // tolerance for an embedded terminator and failed on some Android builds.
    const size_t shader_size = std::min(source_size, std::strlen(source));
    const int compile_status = ncnn::compile_spirv_module(
        source, static_cast<int>(shader_size), options, spirv);
    if (compile_status != 0 || spirv.empty())
        return compile_status != 0 ? compile_status : -1;
    pipeline = new ncnn::Pipeline(device);
    pipeline->set_optimal_local_size_xyz();
    const std::vector<ncnn::vk_specialization_type> specializations;
    return pipeline->create(spirv.data(), spirv.size() * sizeof(uint32_t), specializations);
}

}  // namespace

RifeWarp::RifeWarp() {
    support_vulkan = true;
}

int RifeWarp::create_pipeline(const ncnn::Option& opt) {
    if (vkdev == nullptr)
        return 0;
    int status = compile_pipeline(vkdev, opt, kWarpPack1, sizeof(kWarpPack1), pipeline_pack1_);
    if (status == 0 && opt.use_packing_layout)
        status = compile_pipeline(vkdev, opt, kWarpPack4, sizeof(kWarpPack4), pipeline_pack4_);
    if (status == 0 && opt.use_fp16_packed)
        status = compile_pipeline(vkdev, opt, kWarpPack8, sizeof(kWarpPack8), pipeline_pack8_);
    return status;
}

int RifeWarp::destroy_pipeline(const ncnn::Option&) {
    delete pipeline_pack1_;
    delete pipeline_pack4_;
    delete pipeline_pack8_;
    pipeline_pack1_ = nullptr;
    pipeline_pack4_ = nullptr;
    pipeline_pack8_ = nullptr;
    return 0;
}

int RifeWarp::forward(
    const std::vector<ncnn::Mat>& bottom_blobs,
    std::vector<ncnn::Mat>& top_blobs,
    const ncnn::Option& opt) const {
    if (bottom_blobs.size() != 2 || top_blobs.size() != 1)
        return -1;
    const ncnn::Mat& image = bottom_blobs[0];
    const ncnn::Mat& flow = bottom_blobs[1];
    if (image.empty() || flow.empty() || flow.c < 2 || image.w != flow.w || image.h != flow.h)
        return -1;
    ncnn::Mat& output = top_blobs[0];
    // This layer does not advertise CPU packing support, so ncnn guarantees
    // pack1 input on this overload and inserts conversions around it.
    output.create(image.w, image.h, image.c, static_cast<size_t>(4U), 1, opt.blob_allocator);
    if (output.empty())
        return -100;

#pragma omp parallel for num_threads(opt.num_threads)
    for (int channel = 0; channel < image.c; ++channel) {
        float* destination = output.channel(channel);
        const ncnn::Mat plane = image.channel(channel);
        const float* flow_x = flow.channel(0);
        const float* flow_y = flow.channel(1);
        for (int y = 0; y < image.h; ++y) {
            for (int x = 0; x < image.w; ++x) {
                const float sample_x = static_cast<float>(x) + *flow_x++;
                const float sample_y = static_cast<float>(y) + *flow_y++;
                const int raw_x0 = static_cast<int>(std::floor(sample_x));
                const int raw_y0 = static_cast<int>(std::floor(sample_y));
                const int x0 = std::clamp(raw_x0, 0, image.w - 1);
                const int y0 = std::clamp(raw_y0, 0, image.h - 1);
                const int x1 = std::clamp(raw_x0 + 1, 0, image.w - 1);
                const int y1 = std::clamp(raw_y0 + 1, 0, image.h - 1);
                const float alpha = sample_x - static_cast<float>(x0);
                const float beta = sample_y - static_cast<float>(y0);
                const float a = plane.row(y0)[x0] * (1.0F - alpha) + plane.row(y0)[x1] * alpha;
                const float b = plane.row(y1)[x0] * (1.0F - alpha) + plane.row(y1)[x1] * alpha;
                *destination++ = a * (1.0F - beta) + b * beta;
            }
        }
    }
    return 0;
}

int RifeWarp::forward(
    const std::vector<ncnn::VkMat>& bottom_blobs,
    std::vector<ncnn::VkMat>& top_blobs,
    ncnn::VkCompute& command,
    const ncnn::Option& opt) const {
    if (bottom_blobs.size() != 2 || top_blobs.size() != 1)
        return -1;
    const ncnn::VkMat& image = bottom_blobs[0];
    const ncnn::VkMat& flow = bottom_blobs[1];
    ncnn::VkMat& output = top_blobs[0];
    output.create(image.w, image.h, image.c, image.elemsize, image.elempack, opt.blob_vkallocator);
    if (output.empty())
        return -100;
    std::vector<ncnn::VkMat> bindings{image, flow, output};
    std::vector<ncnn::vk_constant_type> constants(4);
    constants[0].i = output.w;
    constants[1].i = output.h;
    constants[2].i = output.c;
    constants[3].i = output.cstep;
    ncnn::Pipeline* pipeline = output.elempack == 8 ? pipeline_pack8_ :
        (output.elempack == 4 ? pipeline_pack4_ : pipeline_pack1_);
    if (pipeline == nullptr)
        return -1;
    command.record_pipeline(pipeline, bindings, constants, output);
    return 0;
}
