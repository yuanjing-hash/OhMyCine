#!/usr/bin/env python3
"""Export the official MIT RIFE v4.6 checkpoint as a flow/mask-only ONNX graph."""

from __future__ import annotations

import argparse
import hashlib
from pathlib import Path

import onnx
import torch
from torch import nn
from torch.nn import functional as functional


SOURCE_COMMIT = "f6b5132517695127bdb5d5a8c3727e719f0fda22"
SOURCE_ARCHIVE_SHA256 = "52b094d14cf275e925a5ae25381e46f94fab1c232a847dc45117cfd7c89ceec2"
CHECKPOINT_SHA256 = "008646e761f0e67cb77f0c6c44cfe3c3e5a05d9d9465311b9681ca650ce030db"


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        while chunk := source.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


def conv(in_channels: int, out_channels: int, stride: int = 1) -> nn.Sequential:
    return nn.Sequential(
        nn.Conv2d(in_channels, out_channels, 3, stride, 1, bias=True),
        nn.LeakyReLU(0.2, inplace=False),
    )


class ResidualConv(nn.Module):
    def __init__(self, channels: int) -> None:
        super().__init__()
        self.conv = nn.Conv2d(channels, channels, 3, 1, 1, bias=True)
        self.beta = nn.Parameter(torch.ones((1, channels, 1, 1)))
        self.relu = nn.LeakyReLU(0.2, inplace=False)

    def forward(self, value: torch.Tensor) -> torch.Tensor:
        return self.relu(self.conv(value) * self.beta + value)


class InterpolationBlock(nn.Module):
    def __init__(self, in_channels: int, channels: int) -> None:
        super().__init__()
        self.conv0 = nn.Sequential(
            conv(in_channels, channels // 2, 2),
            conv(channels // 2, channels, 2),
        )
        self.convblock = nn.Sequential(*(ResidualConv(channels) for _ in range(8)))
        self.lastconv = nn.Sequential(
            nn.ConvTranspose2d(channels, 24, 4, 2, 1),
            nn.PixelShuffle(2),
        )

    def forward(
        self,
        value: torch.Tensor,
        flow: torch.Tensor | None,
        scale: float,
    ) -> tuple[torch.Tensor, torch.Tensor]:
        value = functional.interpolate(
            value, scale_factor=1.0 / scale, mode="bilinear", align_corners=False
        )
        if flow is not None:
            scaled_flow = functional.interpolate(
                flow, scale_factor=1.0 / scale, mode="bilinear", align_corners=False
            ) / scale
            value = torch.cat((value, scaled_flow), dim=1)
        feature = self.convblock(self.conv0(value))
        result = functional.interpolate(
            self.lastconv(feature), scale_factor=scale, mode="bilinear", align_corners=False
        )
        return result[:, :4] * scale, result[:, 4:5]


def warp(image: torch.Tensor, flow: torch.Tensor) -> torch.Tensor:
    batch, _, height, width = flow.shape
    x = torch.linspace(-1.0, 1.0, width, dtype=image.dtype, device=image.device)
    y = torch.linspace(-1.0, 1.0, height, dtype=image.dtype, device=image.device)
    grid_y, grid_x = torch.meshgrid(y, x, indexing="ij")
    base = torch.stack((grid_x, grid_y), dim=-1).unsqueeze(0).expand(batch, -1, -1, -1)
    normalized_x = flow[:, 0] / ((width - 1.0) / 2.0)
    normalized_y = flow[:, 1] / ((height - 1.0) / 2.0)
    sampling_grid = base + torch.stack((normalized_x, normalized_y), dim=-1)
    return functional.grid_sample(
        image,
        sampling_grid,
        mode="bilinear",
        padding_mode="border",
        align_corners=True,
    )


class RifeFlowMask(nn.Module):
    def __init__(self) -> None:
        super().__init__()
        self.block0 = InterpolationBlock(7, 192)
        self.block1 = InterpolationBlock(12, 128)
        self.block2 = InterpolationBlock(12, 96)
        self.block3 = InterpolationBlock(12, 64)

    def forward(
        self,
        earlier: torch.Tensor,
        later: torch.Tensor,
        timestep: torch.Tensor,
    ) -> tuple[torch.Tensor, torch.Tensor]:
        flow = None
        mask = None
        warped_earlier = earlier
        warped_later = later
        blocks = (self.block0, self.block1, self.block2, self.block3)
        for block, scale in zip(blocks, (8.0, 4.0, 2.0, 1.0)):
            if flow is None:
                flow, mask = block(torch.cat((earlier, later, timestep), dim=1), None, scale)
            else:
                increment, mask_increment = block(
                    torch.cat((warped_earlier, warped_later, timestep, mask), dim=1),
                    flow,
                    scale,
                )
                flow = flow + increment
                mask = mask + mask_increment
            warped_earlier = warp(earlier, flow[:, :2])
            warped_later = warp(later, flow[:, 2:4])
        return flow, torch.sigmoid(mask)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--checkpoint", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    if sha256(args.checkpoint) != CHECKPOINT_SHA256:
        raise RuntimeError("RIFE v4.6 checkpoint checksum mismatch")

    model = RifeFlowMask().eval()
    state = torch.load(args.checkpoint, map_location="cpu", weights_only=True)
    state = {key.removeprefix("module."): value for key, value in state.items()}
    missing, unexpected = model.load_state_dict(state, strict=False)
    if missing or unexpected:
        raise RuntimeError(f"checkpoint schema mismatch: missing={missing}, unexpected={unexpected}")

    args.output.parent.mkdir(parents=True, exist_ok=True)
    size = 64
    earlier = torch.full((1, 3, size, size), 0.2, dtype=torch.float32)
    later = torch.full((1, 3, size, size), 0.8, dtype=torch.float32)
    timestep = torch.full((1, 1, size, size), 0.5, dtype=torch.float32)
    with torch.inference_mode():
        torch.onnx.export(
            model,
            (earlier, later, timestep),
            args.output,
            input_names=("earlier_proxy", "later_proxy", "timestep"),
            output_names=("flow_pixels", "blend_mask"),
            dynamic_axes={
                "earlier_proxy": {2: "height", 3: "width"},
                "later_proxy": {2: "height", 3: "width"},
                "timestep": {2: "height", 3: "width"},
                "flow_pixels": {2: "height", 3: "width"},
                "blend_mask": {2: "height", 3: "width"},
            },
            opset_version=18,
            do_constant_folding=True,
            dynamo=False,
        )

    exported = onnx.load(args.output)
    exported.producer_name = "OhMyCine reproducible RIFE flow/mask exporter"
    exported.metadata_props.add(key="source_commit", value=SOURCE_COMMIT)
    exported.metadata_props.add(key="source_archive_sha256", value=SOURCE_ARCHIVE_SHA256)
    exported.metadata_props.add(key="checkpoint_sha256", value=CHECKPOINT_SHA256)
    exported.metadata_props.add(key="hdr_contract", value="flow/mask only; no SDR RGB composite")
    onnx.checker.check_model(exported, full_check=True)
    onnx.save(exported, args.output)
    print(f"{args.output} sha256={sha256(args.output)}")


if __name__ == "__main__":
    main()
