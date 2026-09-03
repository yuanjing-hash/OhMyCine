package com.ohmycine.player.mpv

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class FrameInterpolationControllerTest {
    @Test
    fun graphicSubtitlePreventsActivation() {
        val controller = FrameInterpolationController()
        controller.setRequested(true)
        controller.onMediaEvent(FrameInterpolationMediaEvent.SURFACE_READY)
        controller.onMediaEvent(FrameInterpolationMediaEvent.FILE_LOADED)
        controller.setGates(hardwareDecode = true, hdrPath = true, backend = true)
        controller.setGraphicSubtitleActive(true)
        assertFalse(controller.backendFirstFrame(controller.snapshot().generation))
        assertEquals("unavailable-graphic-subtitle", controller.snapshot().state)
    }

    @Test
    fun staleFrameCannotActivateAfterSeek() {
        val controller = FrameInterpolationController()
        controller.setRequested(true)
        controller.onMediaEvent(FrameInterpolationMediaEvent.SURFACE_READY)
        controller.setGates(hardwareDecode = true, hdrPath = true, backend = true)
        controller.onMediaEvent(FrameInterpolationMediaEvent.FILE_LOADED)
        val staleGeneration = controller.snapshot().generation
        controller.onMediaEvent(FrameInterpolationMediaEvent.SEEK)
        assertEquals("temporary-bypass", controller.snapshot().state)
        assertFalse(controller.backendFirstFrame(staleGeneration))
        assertTrue(controller.backendFirstFrame(controller.snapshot().generation))
        assertEquals("active", controller.snapshot().state)
    }

    @Test
    fun hardwareDecodeAndHdrAreMandatory() {
        val controller = FrameInterpolationController()
        controller.setRequested(true)
        controller.onMediaEvent(FrameInterpolationMediaEvent.FILE_LOADED)
        controller.onMediaEvent(FrameInterpolationMediaEvent.SURFACE_READY)
        controller.setGates(hardwareDecode = false, hdrPath = true, backend = true)
        assertEquals("unavailable-no-hwdec", controller.snapshot().state)
        controller.setGates(hardwareDecode = true, hdrPath = false, backend = true)
        assertEquals("unavailable-hdr-path", controller.snapshot().state)
    }
}
