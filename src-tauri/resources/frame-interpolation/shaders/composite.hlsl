Texture2D<float4> Source0 : register(t0);
Texture2D<float4> Source1 : register(t1);
Texture2D<float4> Flow : register(t2);
Texture2D<float> Mask : register(t3);
SamplerState LinearClamp : register(s0);
RWTexture2D<float4> Output : register(u0);

cbuffer CompositeParameters : register(b0) {
    uint2 outputSize;
    uint2 flowSize;
    float inverseFlowScale;
    float timestep;
    float confidenceThreshold;
    uint sceneCut;
};

float confidence(float3 earlier, float3 later) {
    float3 a = earlier / (1.0 + max(earlier, 0.0));
    float3 b = later / (1.0 + max(later, 0.0));
    float disagreement = dot(abs(a - b), float3(0.2126, 0.7152, 0.0722));
    return saturate(1.0 - disagreement * 3.0);
}

[numthreads(8, 8, 1)]
void main(uint3 threadId : SV_DispatchThreadID) {
    if (any(threadId.xy >= outputSize))
        return;
    float2 uv = (float2(threadId.xy) + 0.5) / float2(outputSize);
    float4 flow = Flow.SampleLevel(LinearClamp, uv, 0.0) * inverseFlowScale;
    float2 pixelToUv = 1.0 / float2(outputSize);
    float3 unwarped0 = Source0.SampleLevel(LinearClamp, uv, 0.0).rgb;
    float3 unwarped1 = Source1.SampleLevel(LinearClamp, uv, 0.0).rgb;
    float3 warped0 = Source0.SampleLevel(LinearClamp, uv + flow.xy * pixelToUv, 0.0).rgb;
    float3 warped1 = Source1.SampleLevel(LinearClamp, uv + flow.zw * pixelToUv, 0.0).rgb;
    float blend = saturate(Mask.SampleLevel(LinearClamp, uv, 0.0));
    float3 generated = warped0 * blend + warped1 * (1.0 - blend);
    float valid = confidence(warped0, warped1) >= confidenceThreshold && sceneCut == 0;
    float3 nearestReal = timestep < 0.5 ? unwarped0 : unwarped1;
    // Keep scRGB/HDR values in FP16. No 8-bit quantization or implicit SDR clamp occurs here.
    Output[threadId.xy] = float4(clamp(valid ? generated : nearestReal, -65504.0, 65504.0), 1.0);
}
