Texture2D<float4> Source : register(t0);
RWTexture2D<float4> Proxy : register(u0);

cbuffer ProxyParameters : register(b0) {
    uint2 sourceSize;
    uint2 proxySize;
    float referenceWhiteNits;
    float sourcePeakNits;
};

[numthreads(8, 8, 1)]
void main(uint3 threadId : SV_DispatchThreadID) {
    if (any(threadId.xy >= proxySize))
        return;
    float2 uv = (float2(threadId.xy) + 0.5) / float2(proxySize);
    uint2 sourcePixel = min(uint2(uv * float2(sourceSize)), sourceSize - 1);
    float3 linearHdr = max(Source.Load(int3(sourcePixel, 0)).rgb, 0.0);
    float peakScale = max(sourcePeakNits / max(referenceWhiteNits, 1.0), 1.0);
    float denominator = log2(1.0 + peakScale);
    float3 compressed = log2(1.0 + min(linearHdr, peakScale)) / denominator;
    Proxy[threadId.xy] = float4(saturate(compressed), 1.0);
}
